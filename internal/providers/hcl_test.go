package providers

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// writeHCLFixture lays out a miniature Terragrunt repo modeled on a real
// multi-environment layout: shared modules composed by an orchestrator,
// plus an environments tree whose leaf terragrunt.hcl includes a root.hcl.
func writeHCLFixture(t *testing.T, root string) {
	t.Helper()

	write(t, root, "modules/inventory/variables.tf", `
variable "nodes" {
  type = map(any)
}

variable "unused_knob" {
  type    = string
  default = ""
}
`)
	write(t, root, "modules/inventory/main.tf", `
locals {
  controlplane = { for k, v in var.nodes : k => v if v.role == "controlplane" }
}
`)
	write(t, root, "modules/inventory/outputs.tf", `
output "controlplane_nodes" {
  value = local.controlplane
}

output "api_endpoint" {
  value = "https://api.example.com"
}
`)

	write(t, root, "modules/redfish/main.tf", `
variable "nodes" {
  type = map(any)
}

resource "redfish_power" "on" {
  for_each = var.nodes
  name     = each.value.name
}
`)

	write(t, root, "modules/orchestrator/main.tf", `
variable "platform" {
  type = string
}

variable "node_inventory" {
  type = map(any)
}

module "inventory" {
  source = "../inventory"
  nodes  = var.node_inventory
}

module "redfish" {
  source = "../redfish"
  nodes  = module.inventory.controlplane_nodes
}

output "endpoint" {
  value = module.inventory.api_endpoint
}
`)

	write(t, root, "root.hcl", `
locals {
  env_class = "non-prod"
}

remote_state {
  backend = "s3"
  config = {
    bucket = "tf-state"
  }
}

inputs = {
  platform = local.env_class
}
`)

	write(t, root, "environments/non-prod/pod0/vpc/terragrunt.hcl", `
include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "${get_repo_root()}//modules/inventory"
}

inputs = {
  nodes = {}
}
`)

	write(t, root, "environments/non-prod/pod0/ocp1/terragrunt.hcl", `
include "root" {
  path = find_in_parent_folders("root.hcl")
}

dependency "vpc" {
  config_path = "../vpc"
}

terraform {
  source = "${get_repo_root()}//modules/orchestrator"
}

inputs = {
  platform       = "openshift"
  node_inventory = dependency.vpc.outputs.controlplane_nodes
}
`)

	write(t, root, "environments/_local/example.tfvars", "")
}

func indexHCL(t *testing.T, root string) *Graph {
	t.Helper()
	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Fatalf("no files indexed; note: %s", g.Note)
	}
	return g
}

func TestHCLDefinitions(t *testing.T) {
	root := t.TempDir()
	writeHCLFixture(t, root)
	g := indexHCL(t, root)

	wantKinds := map[string]string{
		"modules/inventory/variables.tf::var.nodes":            "variable",
		"modules/inventory/main.tf::local.controlplane":        "local",
		"modules/inventory/outputs.tf::output.api_endpoint":    "output",
		"modules/orchestrator/main.tf::module.inventory":       "module_call",
		"modules/redfish/main.tf::redfish_power.on":            "resource",
		"environments/non-prod/pod0/ocp1/terragrunt.hcl::ocp1": "stack",
		"root.hcl::root": "config",
	}
	for qn, kind := range wantKinds {
		sym, ok := g.Defs[qn]
		if !ok {
			t.Errorf("missing def %s (defs: %v)", qn, defNames(g))
			continue
		}
		if sym.Kind != kind {
			t.Errorf("def %s kind = %q, want %q", qn, sym.Kind, kind)
		}
	}

	if sym := g.Defs["modules/inventory/variables.tf::var.nodes"]; sym != nil && !sym.Public {
		t.Errorf("variables should be Public (module API)")
	}
	if !slices.Contains(g.Langs, "hcl") {
		t.Errorf("langs = %v, want to include hcl", g.Langs)
	}
}

func TestHCLModuleCallEdges(t *testing.T) {
	root := t.TempDir()
	writeHCLFixture(t, root)
	g := indexHCL(t, root)

	modInventory := "modules/orchestrator/main.tf::module.inventory"
	modRedfish := "modules/orchestrator/main.tf::module.redfish"

	// Module-call arguments wire to the target module's variables.
	if got := g.Callees(modInventory); !slices.Contains(got, "modules/inventory/variables.tf::var.nodes") {
		t.Errorf("module.inventory callees = %v, want target var.nodes", got)
	}
	if got := g.Callees(modRedfish); !slices.Contains(got, "modules/redfish/main.tf::var.nodes") {
		t.Errorf("module.redfish callees = %v, want redfish var.nodes", got)
	}

	// module.inventory.controlplane_nodes (read inside the redfish module call)
	// resolves both the local module-call def and the remote output.
	if got := g.Callees(modRedfish); !slices.Contains(got, modInventory) {
		t.Errorf("module.redfish callees = %v, want module.inventory (local hop)", got)
	}
	if got := g.Callees(modRedfish); !slices.Contains(got, "modules/inventory/outputs.tf::output.controlplane_nodes") {
		t.Errorf("module.redfish callees = %v, want inventory output (remote hop)", got)
	}

	// var.x references resolve within the directory (cross-file).
	out := "modules/inventory/outputs.tf::output.controlplane_nodes"
	if got := g.Callees(out); !slices.Contains(got, "modules/inventory/main.tf::local.controlplane") {
		t.Errorf("output.controlplane_nodes callees = %v, want local.controlplane", got)
	}
	loc := "modules/inventory/main.tf::local.controlplane"
	if got := g.Callees(loc); !slices.Contains(got, "modules/inventory/variables.tf::var.nodes") {
		t.Errorf("local.controlplane callees = %v, want var.nodes", got)
	}
}

func TestHCLTerragruntEdges(t *testing.T) {
	root := t.TempDir()
	writeHCLFixture(t, root)
	g := indexHCL(t, root)

	stack := "environments/non-prod/pod0/ocp1/terragrunt.hcl::ocp1"
	callees := g.Callees(stack)

	// include "root" -> root.hcl config layer.
	if !slices.Contains(callees, "root.hcl::root") {
		t.Errorf("stack callees = %v, want root.hcl::root via include", callees)
	}
	// inputs keys -> sourced module's variables.
	if !slices.Contains(callees, "modules/orchestrator/main.tf::var.platform") {
		t.Errorf("stack callees = %v, want orchestrator var.platform via inputs", callees)
	}
	// dependency.vpc usage inside inputs resolves to the dependency def, and the
	// dependency def points at the target stack.
	dep := "environments/non-prod/pod0/ocp1/terragrunt.hcl::dependency.vpc"
	if !slices.Contains(callees, dep) {
		t.Errorf("stack callees = %v, want dependency.vpc", callees)
	}
	if got := g.Callees(dep); !slices.Contains(got, "environments/non-prod/pod0/vpc/terragrunt.hcl::vpc") {
		t.Errorf("dependency.vpc callees = %v, want vpc stack", got)
	}

	// Blast radius: changing the inventory output reaches the vpc stack
	// (terragrunt source) and through orchestrator into the ocp1 chain.
	impact := g.Impact("modules/inventory/outputs.tf::output.controlplane_nodes")
	if !slices.Contains(impact, "modules/orchestrator/main.tf::module.redfish") {
		t.Errorf("impact = %v, want module.redfish", impact)
	}
}

func TestHCLNoGlobalFallback(t *testing.T) {
	root := t.TempDir()
	// var.special defined ONLY in module a; module b references var.special
	// without defining it. Broken Terraform must stay unresolved, not link
	// across modules.
	write(t, root, "a/main.tf", `
variable "special" {
  type = string
}
`)
	write(t, root, "b/main.tf", `
locals {
  v = var.special
}
`)
	g := indexHCL(t, root)
	target := "a/main.tf::var.special"
	if callers := g.Callers(target); len(callers) != 0 {
		t.Errorf("var.special in module a should have no callers, got %v", callers)
	}
	if g.Unresolved == 0 {
		t.Errorf("expected the dangling var.special reference to count as unresolved")
	}
}

func TestHCLCacheRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeHCLFixture(t, root)
	cachePath := root + "/.cache.json"

	g1, err := Builtin{}.IndexWithCache(context.Background(), root, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := Builtin{}.IndexWithCache(context.Background(), root, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if g2.reused == 0 {
		t.Fatalf("second index reused no files (reused=%d retagged=%d)", g2.reused, g2.retagged)
	}
	if len(g1.Defs) != len(g2.Defs) {
		t.Errorf("defs differ across cache round-trip: %d vs %d", len(g1.Defs), len(g2.Defs))
	}
	// Edges must survive the round-trip (imports serialization included).
	stack := "environments/non-prod/pod0/ocp1/terragrunt.hcl::ocp1"
	if !slices.Equal(g1.Callees(stack), g2.Callees(stack)) {
		t.Errorf("stack callees differ: %v vs %v", g1.Callees(stack), g2.Callees(stack))
	}
}

func TestHCLOpenTofuExtension(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.tofu", `
variable "region" {
  type = string
}

locals {
  r = var.region
}
`)
	g := indexHCL(t, root)
	if _, ok := g.Defs["main.tofu::var.region"]; !ok {
		t.Fatalf("missing .tofu def (defs: %v)", defNames(g))
	}
	if got := g.Callees("main.tofu::local.r"); !slices.Contains(got, "main.tofu::var.region") {
		t.Errorf("local.r callees = %v, want var.region", got)
	}
}

func TestHCLLockFileSkipped(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".terraform.lock.hcl", `
provider "registry.terraform.io/hashicorp/null" {
  version = "3.2.2"
}
`)
	write(t, root, "main.tf", `
variable "x" {
  type = string
}
`)
	g := indexHCL(t, root)
	for qn := range g.Defs {
		if strings.Contains(qn, ".terraform.lock.hcl") {
			t.Errorf("lock file leaked into graph: %s", qn)
		}
	}
}

func TestHCLTfvarsAssignsVariables(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.tf", `
variable "region" {
  type = string
}
`)
	write(t, root, "prod.tfvars", `
region = "us-east-1"
`)
	g := indexHCL(t, root)
	callers := g.Callers("main.tf::var.region")
	if !slices.Contains(callers, "prod.tfvars::(top-level)") {
		t.Errorf("var.region callers = %v, want prod.tfvars::(top-level)", callers)
	}
}
