package server

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/scan"
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

func TestStacksTerragruntUnits(t *testing.T) {
	root := terragruntFixture(t)
	out, err := stacksExtract(stacksInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 3 {
		t.Fatalf("total = %d, want 3 (got %+v)", out.Total, out.Stacks)
	}

	var app, vpc, standalone *scan.StackUnit
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

func TestStacksEmptyRepoNote(t *testing.T) {
	root := t.TempDir()
	writeStackFile(t, root, "main.go", "package main\n")
	out, err := stacksExtract(stacksInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 0 || out.Note == "" {
		t.Fatalf("want empty result with advisory note, got %+v", out)
	}
}

func TestStacksModulesOnlyNote(t *testing.T) {
	root := t.TempDir()
	writeStackFile(t, root, "modules/a/main.tf", `variable "x" { type = string }`)
	out, err := stacksExtract(stacksInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 0 {
		t.Fatalf("want 0 stacks for modules-only repo, got %+v", out.Stacks)
	}
	if !strings.Contains(out.Note, "modules-only") {
		t.Errorf("note = %q, want modules-only explanation", out.Note)
	}
}

func TestTerraformManifestParsing(t *testing.T) {
	root := t.TempDir()
	writeStackFile(t, root, "versions.tf", `
terraform {
  required_version = ">= 1.8.0, < 2.0.0"
  required_providers {
    talos = {
      source  = "siderolabs/talos"
      version = "~> 0.6"
    }
    vault = { source = "hashicorp/vault", version = "~> 4.3" }
  }
}
`)
	writeStackFile(t, root, ".terraform.lock.hcl", `
provider "registry.terraform.io/siderolabs/talos" {
  version     = "0.6.1"
  constraints = "~> 0.6"
}
`)
	writeStackFile(t, root, "main.tf", `
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.1.0"
}

module "local_thing" {
  source = "./modules/thing"
}
`)
	out, err := depsExtract(context.Background(), depsInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}

	byManifest := map[string]scan.ManifestDeps{}
	for _, m := range out.Manifests {
		byManifest[m.Manifest] = m
	}

	versions, ok := byManifest["versions.tf"]
	if !ok {
		t.Fatalf("versions.tf not parsed; manifests: %v", manifestNames(out))
	}
	if versions.Engines["terraform"] != ">= 1.8.0, < 2.0.0" {
		t.Errorf("required_version = %v", versions.Engines)
	}
	if len(versions.Direct) != 2 {
		t.Fatalf("provider deps = %+v, want 2", versions.Direct)
	}
	if versions.Direct[0].Name != "hashicorp/vault" || versions.Direct[0].Version != "~> 4.3" {
		t.Errorf("provider[0] = %+v", versions.Direct[0])
	}

	lock, ok := byManifest[".terraform.lock.hcl"]
	if !ok {
		t.Fatalf("lock file not parsed; manifests: %v", manifestNames(out))
	}
	if len(lock.Direct) != 1 || lock.Direct[0].Version != "0.6.1" || lock.Direct[0].Kind != "locked" {
		t.Errorf("lock deps = %+v", lock.Direct)
	}

	main, ok := byManifest["main.tf"]
	if !ok {
		t.Fatalf("main.tf not parsed; manifests: %v", manifestNames(out))
	}
	if len(main.Direct) != 1 || main.Direct[0].Name != "terraform-aws-modules/vpc/aws" || main.Direct[0].Kind != "module" {
		t.Errorf("module deps = %+v, want only the external registry module", main.Direct)
	}
}

func manifestNames(out depsOutput) []string {
	var names []string
	for _, m := range out.Manifests {
		names = append(names, m.Manifest)
	}
	return names
}
