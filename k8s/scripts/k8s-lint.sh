#!/usr/bin/env bash
#
# K8s-specific linting script for YAML and shell scripts
# This separates K8s linting from Boulder linting as specified in requirements
#

set -o errexit
set -o nounset
set -o pipefail

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

function print_colored() {
  local color=$1
  local message=$2
  echo -e "${color}${message}${NC}"
}

function print_heading() {
  echo
  print_colored "$BLUE" "▶ $1"
}

function print_success() {
  print_colored "$GREEN" "✓ $1"
}

function print_error() {
  print_colored "$RED" "✗ $1"
}

function print_warning() {
  print_colored "$YELLOW" "⚠ $1"
}

# Track overall status
LINT_FAILED=false

print_heading "Running Kubernetes-specific lints..."

# Check for yamllint
if command -v yamllint >/dev/null 2>&1; then
  print_heading "Linting YAML files in k8s/ directory..."

  # Find all YAML files in k8s/ directory
  yaml_files=$(find k8s -type f \( -name "*.yaml" -o -name "*.yml" \) 2>/dev/null)

  if [ -n "$yaml_files" ]; then
    # Run yamllint on all YAML files
    if echo "$yaml_files" | xargs yamllint -d relaxed; then
      print_success "YAML linting passed"
    else
      print_error "YAML linting failed"
      LINT_FAILED=true
    fi
  else
    print_warning "No YAML files found in k8s/ directory"
  fi
else
  print_warning "yamllint not found - skipping YAML linting"
  print_warning "Install with: brew install yamllint (macOS) or apt-get install yamllint (Linux)"
fi

# Check for shellcheck
if command -v shellcheck >/dev/null 2>&1; then
  print_heading "Linting shell scripts in k8s/ directory..."

  # Find all shell scripts in k8s/ directory
  shell_files=$(find k8s -type f -name "*.sh" 2>/dev/null)

  if [ -n "$shell_files" ]; then
    # Run shellcheck on all shell scripts
    if echo "$shell_files" | xargs shellcheck; then
      print_success "Shell script linting passed"
    else
      print_error "Shell script linting failed"
      LINT_FAILED=true
    fi
  else
    print_warning "No shell scripts found in k8s/ directory"
  fi
else
  print_warning "shellcheck not found - skipping shell script linting"
  print_warning "Install with: brew install shellcheck (macOS) or apt-get install shellcheck (Linux)"
fi

# Also lint tk8s.sh and tnk8s.sh if they exist
if command -v shellcheck >/dev/null 2>&1; then
  print_heading "Linting tk8s.sh and tnk8s.sh..."
  test_scripts=()
  [ -f "tk8s.sh" ] && test_scripts+=("tk8s.sh")
  [ -f "tnk8s.sh" ] && test_scripts+=("tnk8s.sh")

  if [ ${#test_scripts[@]} -gt 0 ]; then
    if shellcheck "${test_scripts[@]}"; then
      print_success "Test script linting passed"
    else
      print_error "Test script linting failed"
      LINT_FAILED=true
    fi
  fi
fi

# Check for kubectl dry-run validation
if command -v kubectl >/dev/null 2>&1; then
  print_heading "Validating Kubernetes manifests..."

  # Find all YAML files and validate them
  yaml_files=$(find k8s -type f \( -name "*.yaml" -o -name "*.yml" \) 2>/dev/null)

  if [ -n "$yaml_files" ]; then
    validation_failed=false
    for file in $yaml_files; do
      # Skip kustomization files and other non-resource files
      if [[ "$file" == *"kustomization"* ]] || [[ "$file" == *"CLAUDE.md"* ]]; then
        continue
      fi

      if ! kubectl apply --dry-run=client -f "$file" >/dev/null 2>&1; then
        print_error "Validation failed for: $file"
        validation_failed=true
      fi
    done

    if [ "$validation_failed" = false ]; then
      print_success "All Kubernetes manifests are valid"
    else
      LINT_FAILED=true
    fi
  fi
else
  print_warning "kubectl not found - skipping manifest validation"
fi

# Final status
echo
if [ "$LINT_FAILED" = true ]; then
  print_error "K8s linting failed"
  exit 1
else
  print_success "All K8s lints passed!"
  exit 0
fi