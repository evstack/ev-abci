#!/usr/bin/env bash
set -euo pipefail

echo "[patch-app-wiring] Starting app wiring patch for network module"

APP_DIR="/workspace/gm"
APP_GO="${APP_DIR}/app/app.go"
APP_CONFIG_GO="${APP_DIR}/app/app_config.go"

if [[ ! -f "$APP_GO" || ! -f "$APP_CONFIG_GO" ]]; then
  echo "[patch-app-wiring] ERROR: expected files not found: $APP_GO or $APP_CONFIG_GO" >&2
  exit 1
fi

insert_import_if_missing() {
  local file="$1"; shift
  local import_line="$1"; shift

  if grep -qF "$import_line" "$file"; then
    return 0
  fi

  # Insert before closing ) of the first import block
  awk -v newimp="$import_line" '
    BEGIN{added=0; inblock=0}
    /^[[:space:]]*import[[:space:]]*\(/ { inblock=1 }
    {
      if (!added && inblock==1 && $0 ~ /^\)/) {
        print newimp
        added=1
        inblock=0
      }
      print $0
    }
  ' "$file" >"${file}.tmp" && mv "${file}.tmp" "$file"
}

insert_before_marker() {
  local file="$1"; shift
  local marker="$1"; shift
  local line_to_insert="$1"; shift

  if grep -qF "$line_to_insert" "$file"; then
    return 0
  fi
  # Insert the line immediately before the marker
  awk -v marker="$marker" -v newline="$line_to_insert" '
    index($0, marker) { print newline; print $0; next }
    { print $0 }
  ' "$file" >"${file}.tmp" && mv "${file}.tmp" "$file"
}

insert_block_before_marker() {
  local file="$1"; shift
  local marker="$1"; shift
  local block="$1"; shift

  # Idempotence: skip if module already present
  if grep -qE 'appconfig\.WrapAny\(&networkmodulev1\.Module\{\}\)' "$file"; then
    return 0
  fi

  awk -v marker="$marker" -v block="$block" '
    index($0, marker) {
      print block
      print $0
      next
    }
    { print $0 }
  ' "$file" >"${file}.tmp" && mv "${file}.tmp" "$file"
}

insert_block_after_first_match() {
  local file="$1"; shift
  local pattern="$1"; shift
  local block="$1"; shift

  # Idempotence: skip if module already present
  if grep -qE 'appconfig\.WrapAny\(&networkmodulev1\.Module\{\}\)' "$file"; then
    return 0
  fi

  awk -v pat="$pattern" -v block="$block" '
    added==1 { print $0; next }
    $0 ~ pat && added==0 {
      print $0
      print block
      added=1
      next
    }
    { print $0 }
  ' "$file" >"${file}.tmp" && mv "${file}.tmp" "$file"
}

# Insert the module block inside the Modules: []*appv1alpha1.ModuleConfig{ ... }
# just before the closing "}," of that slice. Robust against formatting.
insert_block_into_modules_slice() {
  local file="$1"; shift
  local block="$1"; shift

  # Idempotence: skip if module already present
  if grep -qE 'appconfig\.WrapAny\(&networkmodulev1\.Module\{\}\)' "$file"; then
    return 0
  fi

  awk -v block="$block" '
    BEGIN{inmods=0; depth=0}
    {
      line=$0
      if (!inmods) {
        print line
        if (line ~ /Modules:[[:space:]]*\[[^]]*\][[:space:]]*\*[[:space:]]*appv1alpha1\.ModuleConfig[[:space:]]*\{/){
          inmods=1; depth=1
        }
        next
      }
      # in modules slice: track depth by braces
      # Increase for each '{' and decrease for each '}'
      # But do not let depth go negative
      open=gsub(/\{/,"{"); close=gsub(/\}/,"}")
      # If this line would close the modules slice (depth==1 and next would decrement), insert block before printing
      if (depth==1 && close>open && line ~ /^[[:space:]]*},[[:space:]]*$/) {
        print block
        print line
        inmods=0
        next
      }
      print line
      depth += open - close
    }
  ' "$file" >"${file}.tmp" && mv "${file}.tmp" "$file"
}

echo "[patch-app-wiring] Patching app_config.go imports and module config"

# Imports for network module
insert_import_if_missing "$APP_CONFIG_GO" $'\tnetworkmodulev1 "github.com/evstack/ev-abci/modules/network/module/v1"'
insert_import_if_missing "$APP_CONFIG_GO" $'\tnetworktypes "github.com/evstack/ev-abci/modules/network/types"'
insert_import_if_missing "$APP_CONFIG_GO" $'\t_ "github.com/evstack/ev-abci/modules/network" // import for side-effects'

insert_in_named_list() {
  local file="$1"; shift
  local list_name="$1"; shift
  local item="$1"; shift

  # If already present in the list, skip
  awk -v name="$list_name" -v item="$item" '
    BEGIN{inlist=0; present=0}
    $0 ~ name"[[:space:]]*:[[:space:]]*\[[^]]*\][[:space:]]*\{" { inlist=1 }
    inlist && index($0, item) { present=1 }
    inlist && $0 ~ /^[[:space:]]*\},/ {
      if (!present) {
        print item
      }
      inlist=0
    }
    { print $0 }
  ' "$file" >"${file}.tmp" && mv "${file}.tmp" "$file"
}

# Ensure networktypes.ModuleName is included in these lists
insert_in_named_list "$APP_CONFIG_GO" "BeginBlockers" $'\t\t\t\t\tnetworktypes.ModuleName,'
insert_in_named_list "$APP_CONFIG_GO" "EndBlockers"   $'\t\t\t\t\tnetworktypes.ModuleName,'
insert_in_named_list "$APP_CONFIG_GO" "InitGenesis"   $'\t\t\t\t\tnetworktypes.ModuleName,'

# Also insert before Starport markers as a fallback (covers differing formats)
insert_before_marker "$APP_CONFIG_GO" "# stargate/app/beginBlockers" $'\t\t\t\t\tnetworktypes.ModuleName,'
insert_before_marker "$APP_CONFIG_GO" "# stargate/app/endBlockers"   $'\t\t\t\t\tnetworktypes.ModuleName,'
insert_before_marker "$APP_CONFIG_GO" "# stargate/app/initGenesis"   $'\t\t\t\t\tnetworktypes.ModuleName,'

# As an extra fallback, insert inside InitGenesis just before the "// chain modules" comment
awk -v item=$'\t\t\t\t\tnetworktypes.ModuleName,' '
  BEGIN { inlist=0; inserted=0 }
  /InitGenesis[[:space:]]*:[[:space:]]*\[[^]]*\][[:space:]]*\{/ { inlist=1 }
  inlist && /networktypes\.ModuleName/ { inserted=1 }
  inlist && /\/\/ chain modules/ && !inserted {
    print item
    inserted=1
  }
  { print }
  inlist && /^[[:space:]]*\},/ { inlist=0 }
' "$APP_CONFIG_GO" >"${APP_CONFIG_GO}.tmp" && mv "${APP_CONFIG_GO}.tmp" "$APP_CONFIG_GO"

# Add network module to module list
read -r -d '' NETWORK_MODULE_BLOCK <<'BLOCK' || true
			{
				Name:   networktypes.ModuleName,
				Config: appconfig.WrapAny(&networkmodulev1.Module{}),
			},
BLOCK

# Prefer inserting before the starport scaffolding marker if present.
if grep -q "# stargate/app/moduleConfig" "$APP_CONFIG_GO"; then
  insert_block_before_marker "$APP_CONFIG_GO" "# stargate/app/moduleConfig" "$NETWORK_MODULE_BLOCK"
else
  # Insert heuristically near the end of Modules slice
  insert_block_into_modules_slice "$APP_CONFIG_GO" "$NETWORK_MODULE_BLOCK"
fi

echo "[patch-app-wiring] Patching app.go imports, keeper and DI injection"

# Import keeper
insert_import_if_missing "$APP_GO" $'\tnetworkkeeper "github.com/evstack/ev-abci/modules/network/keeper"'

# Keeper field in App struct
insert_before_marker "$APP_GO" "# stargate/app/keeperDeclaration" $'\tNetworkKeeper networkkeeper.Keeper'

# Add to depinject.Inject argument list
if ! grep -q "&app.NetworkKeeper" "$APP_GO"; then
  sed -i -E '/&app\.ParamsKeeper,/a\
\t\t&app.NetworkKeeper,\
' "$APP_GO" || true
fi

# Add getter if missing
if ! grep -q "GetNetworkKeeper()" "$APP_GO"; then
  cat >>"$APP_GO" <<'EOF'

func (app *App) GetNetworkKeeper() networkkeeper.Keeper {
    return app.NetworkKeeper
}
EOF
fi

echo "[patch-app-wiring] Completed"

# Verify expected insertions; fail early with context if missing
verify() {
  local file="$1"; shift
  local pattern="$1"; shift
  if ! grep -Eq "$pattern" "$file"; then
    echo "[patch-app-wiring] ERROR: verification failed for pattern: $pattern in $file" >&2
    echo "----- BEGIN $file (head) -----" >&2
    sed -n '1,200p' "$file" >&2 || true
    echo "----- END $file (head) -----" >&2
    exit 1
  fi
}

verify "$APP_CONFIG_GO" 'github.com/evstack/ev-abci/modules/network/module/v1'
verify "$APP_CONFIG_GO" 'Config: appconfig.WrapAny\(&networkmodulev1.Module\{\}\)'
verify "$APP_CONFIG_GO" 'Name:[[:space:]]*networktypes.ModuleName'

# Verify networktypes.ModuleName is inside InitGenesis list
if ! awk '
  BEGIN{inlist=0; found=0}
  /InitGenesis[[:space:]]*:[[:space:]]*\[[^]]*\][[:space:]]*\{/ { inlist=1 }
  inlist && /networktypes\.ModuleName/ { found=1 }
  inlist && /^[[:space:]]*\},/ { exit }
  END{ if (!found) exit 1 }
' "$APP_CONFIG_GO"; then
  # Additional targeted fallback: insert right after icatypes.ModuleName,
  sed -i '/icatypes\.ModuleName,/a \	\t\t\t\tnetworktypes.ModuleName,' "$APP_CONFIG_GO" || true
fi

# Re-verify; if still missing, attempt marker-based insertion and cleanups
if ! awk '
  BEGIN{inlist=0; found=0}
  /InitGenesis[[:space:]]*:[[:space:]]*\[[^]]*\][[:space:]]*\{/ { inlist=1 }
  inlist && /networktypes\.ModuleName/ { found=1 }
  inlist && /^[[:space:]]*\},/ { exit }
  END{ if (!found) exit 1 }
' "$APP_CONFIG_GO"; then
  # Last resort: insert before Starport marker inside InitGenesis with real tabs and re-verify
  TAB=$'\t\t\t\t\t'
  sed -i "/# stargate\\\/app\\\/initGenesis/i ${TAB}networktypes.ModuleName," "$APP_CONFIG_GO" || true
  # Clean up any accidental leading 't' characters from prior runs
  sed -i -E 's/^t([[:space:]]+networktypes\.ModuleName,)/\1/' "$APP_CONFIG_GO" || true
  awk '
    BEGIN{inlist=0; found=0}
    /InitGenesis[[:space:]]*:[[:space:]]*\[[^]]*\][[:space:]]*\{/ { inlist=1 }
    inlist && /networktypes\.ModuleName/ { found=1 }
    inlist && /^[[:space:]]*\},/ { exit }
    END{ if (!found) exit 1 }
  ' "$APP_CONFIG_GO" || {
  echo "[patch-app-wiring] ERROR: networktypes.ModuleName not found in InitGenesis list after fallback insertion" >&2
  echo "----- InitGenesis region context -----" >&2
  nl -ba "$APP_CONFIG_GO" | sed -n '/InitGenesis[[:space:]]*:[[:space:]]*\[[^]]*\][[:space:]]*{/,/^[[:space:]]*},/p' >&2 || true
  echo "----- FULL app_config.go (head 500 lines) -----" >&2
  nl -ba "$APP_CONFIG_GO" | sed -n '1,500p' >&2 || true
  echo "----- GREP summary -----" >&2
  (grep -n "InitGenesis\|networktypes.ModuleName\|WrapAny(&networkmodulev1.Module{})" "$APP_CONFIG_GO" >&2 || true)
  exit 1
  }
fi
