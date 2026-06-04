package providers

import (
	"context"
	"slices"
	"testing"
)

func TestRustMethodCallEdges(t *testing.T) {
	root := t.TempDir()
	write(t, root, "lib.rs", `
pub struct Svc { pool: i32 }

impl Svc {
    pub fn new(p: i32) -> Self { Self { pool: p } }

    pub fn run(&self) -> i32 {
        let x = self.helper();
        let y = Svc::new(x);
        other_fn(y.pool)
    }

    fn helper(&self) -> i32 { self.pool }
}

fn other_fn(x: i32) -> i32 { x + 1 }
`)

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Skipf("rust grammar not available: %s", g.Note)
	}

	run := qnameOf(t, g, "run")
	helper := qnameOf(t, g, "helper")
	newFn := qnameOf(t, g, "new")
	otherFn := qnameOf(t, g, "other_fn")

	callees := g.Callees(run)
	for _, want := range []string{helper, newFn, otherFn} {
		if !slices.Contains(callees, want) {
			t.Errorf("run should call %s; got callees=%v", want, callees)
		}
	}

	if !slices.Contains(g.Callers(helper), run) {
		t.Errorf("helper should be called by run; got callers=%v", g.Callers(helper))
	}
}

func TestRustAsyncImplCallGraph(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/service.rs", `
pub struct MyService { pool: i32 }

impl MyService {
    pub async fn save(&self, input: i32) -> i32 {
        let p = self.load(input);
        self.persist(p)
    }

    async fn load(&self, id: i32) -> i32 { id }
    async fn persist(&self, val: i32) -> i32 { val }
}
`)

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Skipf("rust grammar not available: %s", g.Note)
	}

	save := qnameOf(t, g, "save")
	load := qnameOf(t, g, "load")
	persist := qnameOf(t, g, "persist")

	callees := g.Callees(save)
	if !slices.Contains(callees, load) {
		t.Errorf("save should call load; callees=%v", callees)
	}
	if !slices.Contains(callees, persist) {
		t.Errorf("save should call persist; callees=%v", callees)
	}
}

func TestRustTraitImplCallGraph(t *testing.T) {
	root := t.TempDir()
	write(t, root, "lib.rs", `
pub trait Handler {
    fn handle(&self) -> i32;
}

pub struct Impl;

impl Handler for Impl {
    fn handle(&self) -> i32 {
        self.internal()
    }
}

impl Impl {
    fn internal(&self) -> i32 { 42 }
}
`)

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Skipf("rust grammar not available: %s", g.Note)
	}

	handles := g.FindSymbols("handle")
	if len(handles) == 0 {
		t.Fatal("handle not found")
	}

	var traitImpl *Symbol
	for _, s := range handles {
		if s.Recv != "" && s.Recv != "Handler" {
			traitImpl = s
			break
		}
	}
	if traitImpl == nil {
		t.Fatal("trait impl handle not found")
	}

	internal := qnameOf(t, g, "internal")
	if !slices.Contains(g.Callees(traitImpl.QName), internal) {
		t.Errorf("handle should call internal; callees=%v", g.Callees(traitImpl.QName))
	}
}

func TestRustMacroCallEdge(t *testing.T) {
	root := t.TempDir()
	write(t, root, "lib.rs", `
macro_rules! greet {
    () => { println!("hi") };
}

fn hello() {
    greet!();
}
`)

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Skipf("rust grammar not available: %s", g.Note)
	}

	hello := qnameOf(t, g, "hello")
	greet := qnameOf(t, g, "greet")
	if !slices.Contains(g.Callees(hello), greet) {
		t.Errorf("hello should call greet!; callees=%v", g.Callees(hello))
	}
}

func TestFindSymbolsByDisplayName(t *testing.T) {
	root := t.TempDir()
	write(t, root, "lib.rs", `
pub struct Engine;
impl Engine {
    pub fn new() -> Self { Engine }
    pub fn run(&self) {}
}
`)

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Skipf("rust grammar not available: %s", g.Note)
	}

	syms := g.FindSymbols("Engine::new")
	if len(syms) == 0 {
		t.Fatal("FindSymbols(\"Engine::new\") should match via Display name")
	}
	if syms[0].Name != "new" || syms[0].Recv != "Engine" {
		t.Errorf("expected Engine::new, got Name=%q Recv=%q", syms[0].Name, syms[0].Recv)
	}

	syms = g.FindSymbols("Engine::run")
	if len(syms) == 0 {
		t.Fatal("FindSymbols(\"Engine::run\") should match via Display name")
	}
}
