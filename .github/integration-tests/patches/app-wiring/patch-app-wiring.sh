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

  # Basic idempotence: if any unique token of the block already exists, skip
  if echo "$block" | while read -r ln; do [[ -n "$ln" ]] && grep -qF "$ln" "$file" && exit 0; done; then
    # found a line; assume present
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

  # If any unique token of the block already exists, skip
  if echo "$block" | while read -r ln; do [[ -n "$ln" ]] && grep -qF "$ln" "$file" && exit 0; done; then
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

echo "[patch-app-wiring] Patching app_config.go imports and module config"

# Imports for network module
insert_import_if_missing "$APP_CONFIG_GO" $'\tnetworkmodulev1 "github.com/evstack/ev-abci/modules/network/module/v1"'
insert_import_if_missing "$APP_CONFIG_GO" $'\tnetworktypes "github.com/evstack/ev-abci/modules/network/types"'
insert_import_if_missing "$APP_CONFIG_GO" $'\t_ "github.com/evstack/ev-abci/modules/network" // import for side-effects'

# Add network module to BeginBlockers, EndBlockers, InitGenesis
insert_before_marker "$APP_CONFIG_GO" "# stargate/app/beginBlockers" $'\t\t\t\t\tnetworktypes.ModuleName,'
insert_before_marker "$APP_CONFIG_GO" "# stargate/app/endBlockers" $'\t\t\t\t\tnetworktypes.ModuleName,'
insert_before_marker "$APP_CONFIG_GO" "# stargate/app/initGenesis" $'\t\t\t\t\tnetworktypes.ModuleName,'

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
fi

# Also ensure insertion after the Modules: []*appv1alpha1.ModuleConfig{ line (robust match),
# to cover templates without the starport marker.
insert_block_after_first_match \
  "$APP_CONFIG_GO" \
  'Modules:[[:space:]]*\[\][[:space:]]*\*?[[:space:]]*appv1alpha1\.ModuleConfig[[:space:]]*\{' \
  "$NETWORK_MODULE_BLOCK"

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
verify "$APP_CONFIG_GO" 'BeginBlockers:[^\n]*\{[\s\S]*networktypes.ModuleName'
verify "$APP_CONFIG_GO" 'EndBlockers:[^\n]*\{[\s\S]*networktypes.ModuleName'
verify "$APP_CONFIG_GO" 'InitGenesis:[^\n]*\{[\s\S]*networktypes.ModuleName'
