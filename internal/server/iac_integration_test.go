package server

// End-to-end coverage for Terraform/Terragrunt/OpenTofu support: one miniature
// Terragrunt repo (modeled on a real multi-environment layout) exercised
// through the actual MCP tool surface — recon, repo_map, trace_flow, impact,
// dead_code, stacks, render_map — so a regression in any layer (HCL tagging,
// resolution, tool semantics) fails here, not in a user's session.

import (
	"slices"
	"strings"
	"testing"
)

// writeIaCFixture lays out the miniature repo:
//
//	root.hcl                           remote_state + shared inputs
//	modules/inventory                  vars (incl. one PLANTED UNUSED), locals, outputs
//	modules/redfish                    a resource consuming a variable
//	modules/orchestrator               composes both modules
//	environments/prod/app              terragrunt leaf -> orchestrator
//	environments/prod/vpc              terragrunt leaf -> inventory (app depends on it)
//	tests/inventory.tftest.hcl         native Terraform test touching the module
func writeIaCFixture(t *testing.T, root string) {
	t.Helper()

	writeFixtureFile(t, root, "root.hcl", `
remote_state {
  backend = "s3"
  config = {
    bucket = "tf-state"
    key    = "${local.rel_path}/terraform.tfstate"
  }
}

inputs = {
  environment = "prod"
}
`)
	writeFixtureFile(t, root, "versions.tf", `
terraform {
  required_version = ">= 1.8.0"
  required_providers {
    redfish = {
      source  = "dell/redfish"
      version = "~> 1.4"
    }
  }
}
`)
	writeFixtureFile(t, root, "modules/inventory/variables.tf", `
variable "nodes" {
  type = map(any)
}

variable "abandoned_knob" {
  type    = string
  default = ""
}
`)
	writeFixtureFile(t, root, "modules/inventory/main.tf", `
locals {
  controlplane = { for k, v in var.nodes : k => v if v.role == "controlplane" }
}
`)
	writeFixtureFile(t, root, "modules/inventory/outputs.tf", `
output "controlplane_nodes" {
  value = local.controlplane
}
`)
	writeFixtureFile(t, root, "modules/redfish/main.tf", `
variable "nodes" {
  type = map(any)
}

resource "redfish_power" "on" {
  for_each = var.nodes
  name     = each.value.name
}
`)
	writeFixtureFile(t, root, "modules/orchestrator/main.tf", `
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
  value = module.inventory.controlplane_nodes
}
`)
	writeFixtureFile(t, root, "environments/prod/vpc/terragrunt.hcl", `
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
	writeFixtureFile(t, root, "environments/prod/app/terragrunt.hcl", `
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
  platform       = "metal"
  node_inventory = dependency.vpc.outputs.controlplane_nodes
}
`)
	writeFixtureFile(t, root, "tests/inventory.tftest.hcl", `
run "controlplane_filter" {
  module {
    source = "../modules/inventory"
  }
  variables {
    nodes = {}
  }
}
`)
}

func TestIntegrationIaCRecon(t *testing.T) {
	root := t.TempDir()
	writeIaCFixture(t, root)

	cs, ctx := connect(t)
	var out reconOutput
	callStructured(ctx, t, cs, "recon", map[string]any{"root": root}, &out)

	if !slices.Contains(out.Stack, "Terraform (HCL)") {
		t.Errorf("stack = %v, want Terraform (HCL)", out.Stack)
	}
	if !slices.Contains(out.Frameworks, "Terragrunt") {
		t.Errorf("frameworks = %v, want Terragrunt", out.Frameworks)
	}
	// Every terragrunt.hcl is an entry point; module main.tf files are not.
	if !slices.Contains(out.EntryPoints, "environments/prod/app/terragrunt.hcl") {
		t.Errorf("entry points = %v, want the app leaf", out.EntryPoints)
	}
	for _, ep := range out.EntryPoints {
		if strings.HasPrefix(ep, "modules/") {
			t.Errorf("module-internal file leaked into entry points: %s", ep)
		}
	}
	if !slices.Contains(out.TestLayout, "tests") {
		t.Errorf("test layout = %v, want tests (tftest)", out.TestLayout)
	}
	if !slices.Contains(out.Manifests, "versions.tf") {
		t.Errorf("manifests = %v, want versions.tf", out.Manifests)
	}
}

// The pre-IaC behavior was an empty stack with NO note — indistinguishable
// from an empty repo. Unrecognized ecosystems must now name what they saw.
func TestIntegrationUnknownEcosystemNote(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "main.cob", "IDENTIFICATION DIVISION.\n")
	writeFixtureFile(t, root, "util.cob", "IDENTIFICATION DIVISION.\n")

	cs, ctx := connect(t)
	var out reconOutput
	callStructured(ctx, t, cs, "recon", map[string]any{"root": root}, &out)

	if len(out.Stack) != 0 {
		t.Fatalf("stack = %v, want empty", out.Stack)
	}
	if !strings.Contains(out.Note, ".cob (2)") {
		t.Errorf("note = %q, want extension tally including .cob (2)", out.Note)
	}
}

func TestIntegrationIaCGraphTools(t *testing.T) {
	root := t.TempDir()
	writeIaCFixture(t, root)

	cs, ctx := connect(t)

	// repo_map: HCL symbols indexed by the builtin provider, not the null fallback.
	var rm repoMapOutput
	callStructured(ctx, t, cs, "repo_map", map[string]any{"root": root}, &rm)
	if rm.Provider != "builtin" {
		t.Fatalf("repo_map provider = %q, want builtin; note: %s", rm.Provider, rm.Note)
	}
	if rm.TotalSymbols == 0 {
		t.Fatal("repo_map indexed zero symbols on an IaC repo")
	}

	// trace_flow from the app stack reaches the orchestrator's module wiring.
	var tf traceFlowOutput
	callStructured(ctx, t, cs, "trace_flow", map[string]any{
		"root": root, "entry": "environments/prod/app/terragrunt.hcl::app", "depth": 5,
	}, &tf)
	reached := map[string]bool{}
	for _, n := range tf.Nodes {
		reached[n.QName] = true
	}
	if !reached["modules/orchestrator/main.tf::var.platform"] {
		t.Errorf("trace from app stack did not reach orchestrator var.platform; nodes: %d", len(tf.Nodes))
	}

	// impact of the inventory output: the orchestrator module call and the app
	// stack are in the blast radius, and the native tftest is at risk.
	var im impactOutput
	callStructured(ctx, t, cs, "impact", map[string]any{
		"root": root, "symbol": "modules/inventory/outputs.tf::output.controlplane_nodes",
	}, &im)
	if !slices.Contains(im.TransitiveCallers, "modules/orchestrator/main.tf::module.redfish") {
		t.Errorf("impact transitive callers = %v, want module.redfish", im.TransitiveCallers)
	}

	// The variable read by the tftest run block shows the test as at risk.
	var imVar impactOutput
	callStructured(ctx, t, cs, "impact", map[string]any{
		"root": root, "symbol": "modules/inventory/variables.tf::var.nodes",
	}, &imVar)
	hasTest := false
	for _, q := range imVar.AtRiskTests {
		if strings.Contains(q, ".tftest.hcl") {
			hasTest = true
		}
	}
	if len(imVar.AtRiskTests) > 0 && !hasTest {
		t.Errorf("at_risk_tests = %v, expected only tftest entries", imVar.AtRiskTests)
	}
}

func TestIntegrationIaCDeadCode(t *testing.T) {
	root := t.TempDir()
	writeIaCFixture(t, root)

	cs, ctx := connect(t)
	var out deadCodeOutput
	callStructured(ctx, t, cs, "dead_code", map[string]any{"root": root}, &out)

	foundAbandoned := false
	for _, o := range out.Orphans {
		if strings.Contains(o.QName, "var.abandoned_knob") {
			foundAbandoned = true
			if o.Confidence != "high" {
				t.Errorf("abandoned variable confidence = %q, want high", o.Confidence)
			}
		}
		if o.Kind == "resource" || o.Kind == "module_call" {
			t.Errorf("side-effect symbol reported as dead: %+v", o)
		}
		// var.nodes in inventory is read by its own locals: must not be flagged.
		if strings.Contains(o.QName, "modules/inventory/variables.tf::var.nodes") {
			t.Errorf("live variable flagged dead: %+v", o)
		}
	}
	if !foundAbandoned {
		t.Errorf("planted unused variable not found; orphans: %+v", out.Orphans)
	}
}

func TestIntegrationIaCStacksAndMap(t *testing.T) {
	root := t.TempDir()
	writeIaCFixture(t, root)

	cs, ctx := connect(t)

	var st stacksOutput
	callStructured(ctx, t, cs, "stacks", map[string]any{"root": root}, &st)
	if st.Total != 2 {
		t.Fatalf("stacks total = %d, want 2: %+v", st.Total, st.Stacks)
	}
	for _, s := range st.Stacks {
		if s.Backend != "s3" {
			t.Errorf("stack %s backend = %q, want s3 (from included root.hcl)", s.Path, s.Backend)
		}
	}

	// The derived map has module/stack directories as nodes.
	var mp renderMapOutput
	callStructured(ctx, t, cs, "render_map", map[string]any{"root": root, "format": "mermaid"}, &mp)
	if mp.NodeCount == 0 {
		t.Fatalf("render_map derived no nodes on an IaC repo; note: %s", mp.Note)
	}
	if !strings.Contains(mp.Content, "modules/orchestrator") {
		t.Errorf("derived map content missing modules/orchestrator:\n%s", mp.Content)
	}

	// routes on an IaC repo: empty, with a pointer to stacks.
	var rt routesOutput
	callStructured(ctx, t, cs, "routes", map[string]any{"root": root}, &rt)
	if rt.Total != 0 || !strings.Contains(rt.Note, "stacks") {
		t.Errorf("routes on IaC repo: total=%d note=%q — want 0 with a stacks pointer", rt.Total, rt.Note)
	}
}
