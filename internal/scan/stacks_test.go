package scan

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeStackFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func terragruntFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeStackFile(t, root, "root.hcl", `
remote_state {
  backend = "s3"
  config = {
    bucket = "tf-state"
    key    = "${local.rel_path}/terraform.tfstate"
  }
}

inputs = {
  environment = "shared"
}
`)
	writeStackFile(t, root, "modules/orchestrator/main.tf", `
variable "platform" {
  type = string
}
`)
	writeStackFile(t, root, "envs/prod/app/terragrunt.hcl", `
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
  platform = "openshift"
  nodes = {
    a = 1
  }
}
`)
	writeStackFile(t, root, "envs/prod/vpc/terragrunt.hcl", `
include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "git::https://example.com/modules/vpc.git?ref=v1.2.0"
}
`)
	writeStackFile(t, root, "standalone/backend.tf", `
terraform {
  backend "gcs" {
    bucket = "state-bucket"
    prefix = "standalone"
  }
}
`)
	return root
}

func TestExtractStacksTerragruntUnits(t *testing.T) {
	root := terragruntFixture(t)
	out := ExtractStacks(root)
	if out.Total != 3 {
		t.Fatalf("total = %d, want 3 (got %+v)", out.Total, out.Stacks)
	}

	var app, vpc, standalone *StackUnit
	for i := range out.Stacks {
		switch out.Stacks[i].Path {
		case "envs/prod/app":
			app = &out.Stacks[i]
		case "envs/prod/vpc":
			vpc = &out.Stacks[i]
		case "standalone":
			standalone = &out.Stacks[i]
		}
	}
	if app == nil || vpc == nil || standalone == nil {
		t.Fatalf("missing expected stacks: %+v", out.Stacks)
	}

	if app.Kind != "terragrunt" || app.Source != "modules/orchestrator" || !app.SourceLocal {
		t.Errorf("app source = %q local=%v, want modules/orchestrator local=true", app.Source, app.SourceLocal)
	}
	if !slices.Contains(app.Includes, "root.hcl") {
		t.Errorf("app includes = %v, want root.hcl", app.Includes)
	}
	if !slices.Contains(app.Dependencies, "envs/prod/vpc") {
		t.Errorf("app dependencies = %v, want envs/prod/vpc", app.Dependencies)
	}
	if app.Backend != "s3" || app.StateKey != "${local.rel_path}/terraform.tfstate" {
		t.Errorf("app backend=%q key=%q", app.Backend, app.StateKey)
	}
	for _, want := range []string{"platform", "nodes", "environment"} {
		if !slices.Contains(app.Inputs, want) {
			t.Errorf("app inputs = %v, want %s", app.Inputs, want)
		}
	}
	if slices.Contains(app.Inputs, "a") {
		t.Errorf("nested object key leaked into inputs: %v", app.Inputs)
	}

	if vpc.SourceLocal || vpc.Source != "git::https://example.com/modules/vpc.git?ref=v1.2.0" {
		t.Errorf("vpc source = %q local=%v, want raw git source, local=false", vpc.Source, vpc.SourceLocal)
	}

	if standalone.Kind != "terraform-root" || standalone.Backend != "gcs" {
		t.Errorf("standalone = %+v, want terraform-root with gcs backend", standalone)
	}
}

func TestExtractStacksModulesOnlyNote(t *testing.T) {
	root := t.TempDir()
	writeStackFile(t, root, "modules/a/main.tf", `variable "x" { type = string }`)
	out := ExtractStacks(root)
	if out.Total != 0 {
		t.Fatalf("want 0 stacks for modules-only repo, got %+v", out.Stacks)
	}
	if !strings.Contains(out.Note, "modules-only") {
		t.Errorf("note = %q, want modules-only explanation", out.Note)
	}
}