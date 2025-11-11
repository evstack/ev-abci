#!/usr/bin/env bash
set -euo pipefail

echo "[patch-app-wiring] Starting app wiring patch for network module and attester command (v3)"

# Resolve APP_DIR
APP_DIR=${APP_DIR:-}
if [[ -z "${APP_DIR}" ]]; then
  candidates=("/workspace/gm" "$PWD/gm")
  if command -v git >/dev/null 2>&1; then
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null || echo "")
    if [[ -n "$repo_root" ]]; then
      candidates+=("$repo_root/gm")
    fi
  fi
  for d in "${candidates[@]}"; do
    if [[ -d "$d" ]]; then APP_DIR="$d"; break; fi
  done
fi

APP_GO="${APP_DIR}/app/app.go"
APP_CONFIG_GO="${APP_DIR}/app/app_config.go"
COMMANDS_GO="${APP_DIR}/cmd/gmd/cmd/commands.go"

if [[ ! -f "$APP_GO" || ! -f "$APP_CONFIG_GO" ]]; then
  echo "[patch-app-wiring] ERROR: expected files not found: $APP_GO or $APP_CONFIG_GO (APP_DIR=$APP_DIR)" >&2
  exit 1
fi

if [[ ! -f "$COMMANDS_GO" ]]; then
  echo "[patch-app-wiring] WARNING: commands.go not found at $COMMANDS_GO, skipping attester command wiring" >&2
fi

# Function to add import if missing
add_import() {
  local file="$1"
  local import_line="$2"
  local import_path=$(echo "$import_line" | sed 's|.*"\(.*\)".*|\1|')

  if grep -qF "\"$import_path\"" "$file"; then
    echo "[patch-app-wiring] Import already exists: $import_path"
    return 0
  fi

  echo "[patch-app-wiring] Adding import: $import_line"
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

# Function to add module to list if not present
add_to_list() {
  local file="$1"
  local list_name="$2"
  local module_name="networktypes.ModuleName"

  # Check if already in the specific list
  if awk -v list="$list_name" -v module="$module_name" '
    BEGIN { in_list=0; found=0 }
    $0 ~ list".*:.*\\[" { in_list=1 }
    in_list && $0 ~ module { found=1; exit }
    in_list && /^[[:space:]]*\},/ { in_list=0 }
    END { exit (found ? 0 : 1) }
  ' "$file"; then
    echo "[patch-app-wiring] $module_name already in $list_name"
    return 0
  fi

  echo "[patch-app-wiring] Adding $module_name to $list_name"

  # Insert before stargate marker - note: Ignite uses camelCase for these markers
  local marker_name=""
  case "$list_name" in
    "BeginBlockers")
      marker_name="beginBlockers"
      ;;
    "EndBlockers")
      marker_name="endBlockers"
      ;;
    "InitGenesis")
      marker_name="initGenesis"
      ;;
  esac
  local marker="# stargate/app/${marker_name}"

  # Debug: show what we're looking for
  echo "[patch-app-wiring] Looking for marker: $marker"

  if ! grep -q "$marker" "$file"; then
    echo "[patch-app-wiring] WARNING: Marker not found: $marker"
    # Fallback: insert after "// chain modules"
    if grep -q "// chain modules" "$file"; then
      echo "[patch-app-wiring] Using fallback: inserting after '// chain modules'"
      awk -v module="$module_name" '
        /\/\/ chain modules/ && !added {
          print
          printf "\t\t\t\t\t%s,\n", module
          added=1
          next
        }
        { print }
      ' "$file" > "${file}.tmp" && mv "${file}.tmp" "$file"
    fi
  else
    awk -v marker="$marker" -v module="$module_name" '
      BEGIN { added=0 }
      $0 ~ marker && !added {
        printf "\t\t\t\t\t%s,\n", module
        added=1
      }
      { print }
    ' "$file" > "${file}.tmp" && mv "${file}.tmp" "$file"
  fi
}

echo "[patch-app-wiring] Step 1: Adding imports to app_config.go"
add_import "$APP_CONFIG_GO" $'\tnetworkmodulev1 "github.com/evstack/ev-abci/modules/network/module/v1"'
add_import "$APP_CONFIG_GO" $'\tnetworktypes "github.com/evstack/ev-abci/modules/network/types"'
add_import "$APP_CONFIG_GO" $'\t_ "github.com/evstack/ev-abci/modules/network" // import for side-effects'

echo "[patch-app-wiring] Step 2: Adding network module to ordering lists"
add_to_list "$APP_CONFIG_GO" "BeginBlockers"
add_to_list "$APP_CONFIG_GO" "EndBlockers"
add_to_list "$APP_CONFIG_GO" "InitGenesis"

echo "[patch-app-wiring] Step 3: Adding network module configuration"
if ! grep -q "Name:.*networktypes.ModuleName" "$APP_CONFIG_GO"; then
  echo "[patch-app-wiring] Adding network module to Modules array"

  # Create the module block
  MODULE_BLOCK="		{
			Name:   networktypes.ModuleName,
			Config: appconfig.WrapAny(&networkmodulev1.Module{}),
		},"

  # Insert before stargate marker
  awk -v block="$MODULE_BLOCK" '
    /# stargate\/app\/moduleConfig/ && !added {
      print block
      added=1
    }
    { print }
  ' "$APP_CONFIG_GO" > "${APP_CONFIG_GO}.tmp" && mv "${APP_CONFIG_GO}.tmp" "$APP_CONFIG_GO"
else
  echo "[patch-app-wiring] Network module already in Modules array"
fi

echo "[patch-app-wiring] Step 4: Patching app.go"
add_import "$APP_GO" $'\tnetworkkeeper "github.com/evstack/ev-abci/modules/network/keeper"'
add_import "$APP_GO" $'\t_ "github.com/evstack/ev-abci/modules/network" // import for module registration'

# Add keeper field
if ! grep -q "NetworkKeeper networkkeeper.Keeper" "$APP_GO"; then
  echo "[patch-app-wiring] Adding NetworkKeeper field"
  awk '/# stargate\/app\/keeperDeclaration/ {
    print "\tNetworkKeeper networkkeeper.Keeper"
  }
  { print }' "$APP_GO" > "${APP_GO}.tmp" && mv "${APP_GO}.tmp" "$APP_GO"
fi

# Add to depinject.Inject
if ! grep -q "&app.NetworkKeeper" "$APP_GO"; then
  echo "[patch-app-wiring] Adding NetworkKeeper to depinject.Inject"
  awk '/&app\.ParamsKeeper,/ {
    print;
    print "\t\t&app.NetworkKeeper,";
    next
  }
  { print }' "$APP_GO" > "${APP_GO}.tmp" && mv "${APP_GO}.tmp" "$APP_GO"
fi

# Add getter method
if ! grep -q "GetNetworkKeeper()" "$APP_GO"; then
  echo "[patch-app-wiring] Adding GetNetworkKeeper method"
  cat >>"$APP_GO" <<'EOF'

func (app *App) GetNetworkKeeper() networkkeeper.Keeper {
    return app.NetworkKeeper
}
EOF
fi

echo "[patch-app-wiring] Step 5: Final validation"

# Validate critical components
errors=0

if ! grep -q '_ "github.com/evstack/ev-abci/modules/network"' "$APP_CONFIG_GO"; then
  echo "[patch-app-wiring] ERROR: Network module import missing in app_config.go!" >&2
  errors=$((errors + 1))
fi

if ! grep -q "Name:.*networktypes.ModuleName" "$APP_CONFIG_GO"; then
  echo "[patch-app-wiring] ERROR: Network module not in Modules array!" >&2
  errors=$((errors + 1))
fi

# Check each list
for list in BeginBlockers EndBlockers InitGenesis; do
  if ! awk -v list="$list" '
    BEGIN { in_list=0; found=0 }
    $0 ~ list".*:.*\\[" { in_list=1 }
    in_list && /networktypes\.ModuleName/ { found=1; exit }
    in_list && /^[[:space:]]*\},/ { in_list=0 }
    END { exit (found ? 0 : 1) }
  ' "$APP_CONFIG_GO"; then
    echo "[patch-app-wiring] ERROR: Network module not in $list!" >&2
    errors=$((errors + 1))
  fi
done

if [[ $errors -gt 0 ]]; then
  echo "[patch-app-wiring] Validation failed with $errors errors!" >&2
  exit 1
fi

echo "[patch-app-wiring] All validations passed successfully!"

# Step 6: Wire attester command to commands.go
if [[ -f "$COMMANDS_GO" ]]; then
  echo "[patch-app-wiring] Step 6: Wiring attester command to commands.go"

  # Add import for abciserver if not present
  if ! grep -q '"github.com/evstack/ev-abci/server"' "$COMMANDS_GO"; then
    echo "[patch-app-wiring] Adding abciserver import to commands.go"
    add_import "$COMMANDS_GO" $'\tabciserver "github.com/evstack/ev-abci/server"'
  else
    echo "[patch-app-wiring] abciserver import already exists in commands.go"
  fi

  # Add attester command to rootCmd
  if ! grep -q "abciserver.NewAttesterCmd()" "$COMMANDS_GO"; then
    echo "[patch-app-wiring] Adding attester command to rootCmd"

    # Find the line with AddCommand(evNodeRollbackCmd) and add after it
    if grep -q "rootCmd.AddCommand(evNodeRollbackCmd)" "$COMMANDS_GO"; then
      awk '
        /rootCmd\.AddCommand\(evNodeRollbackCmd\)/ {
          print
          print "\trootCmd.AddCommand(abciserver.NewAttesterCmd())"
          next
        }
        { print }
      ' "$COMMANDS_GO" > "${COMMANDS_GO}.tmp" && mv "${COMMANDS_GO}.tmp" "$COMMANDS_GO"
      echo "[patch-app-wiring] Attester command added successfully"
    else
      echo "[patch-app-wiring] WARNING: Could not find AddCommand(evNodeRollbackCmd) in commands.go"
      echo "[patch-app-wiring] Attempting to add at end of initRootCmd function"

      # Fallback: add before the closing brace of initRootCmd function
      awk '
        /^}$/ && in_initRootCmd {
          print "\trootCmd.AddCommand(abciserver.NewAttesterCmd())"
          in_initRootCmd=0
        }
        /^func initRootCmd\(/ { in_initRootCmd=1 }
        { print }
      ' "$COMMANDS_GO" > "${COMMANDS_GO}.tmp" && mv "${COMMANDS_GO}.tmp" "$COMMANDS_GO"
    fi
  else
    echo "[patch-app-wiring] Attester command already wired in commands.go"
  fi

  # Validate attester command wiring
  if ! grep -q "abciserver.NewAttesterCmd()" "$COMMANDS_GO"; then
    echo "[patch-app-wiring] ERROR: Failed to wire attester command!" >&2
    errors=$((errors + 1))
  else
    echo "[patch-app-wiring] Attester command wiring validated successfully"
  fi
else
  echo "[patch-app-wiring] Skipping attester command wiring (commands.go not found)"
fi

if [[ $errors -gt 0 ]]; then
  echo "[patch-app-wiring] Patch completed with $errors validation errors!" >&2
  exit 1
fi

echo "[patch-app-wiring] Patch completed successfully"
