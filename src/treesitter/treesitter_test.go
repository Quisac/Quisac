package treesitter

import (
	"testing"
)

func TestParsePython(t *testing.T) {
	code := []byte(`
@route("/api")
def handler():
    db.query()
`)
	extracted, err := ParseCode(code, ".py", "test.py")
	if err != nil {
		t.Fatalf("ParseCode failed: %v", err)
	}
	if len(extracted.Functions) != 1 || extracted.Functions[0] != "handler" {
		t.Errorf("Expected function 'handler', got %v", extracted.Functions)
	}
	if len(extracted.DecoratorUses) != 1 || extracted.DecoratorUses[0].Name != "route(\"/api\")" {
		t.Errorf("Expected decorator 'route(\"/api\")', got %v", extracted.DecoratorUses)
	}
	if len(extracted.Calls) != 1 || extracted.Calls[0].Target != "db.query" {
		t.Errorf("Expected call 'db.query', got %v", extracted.Calls)
	}
	if extracted.Calls[0].CallerScope != "func:handler" {
		t.Errorf("Expected CallerScope 'func:handler', got %v", extracted.Calls[0].CallerScope)
	}
}

func TestParseJavaScript(t *testing.T) {
	code := []byte(`
import { UserService } from './user';
class App {
    start() {
        const svc = new UserService();
        svc.init();
    }
}
`)
	extracted, err := ParseCode(code, ".js", "test.js")
	if err != nil {
		t.Fatalf("ParseCode failed: %v", err)
	}
	if len(extracted.Imports) != 1 {
		t.Errorf("Expected 1 import, got %d", len(extracted.Imports))
	}
	if len(extracted.Classes) != 1 || extracted.Classes[0] != "App" {
		t.Errorf("Expected class 'App', got %v", extracted.Classes)
	}
	if len(extracted.Methods) != 1 || extracted.Methods[0] != "App.start" {
		t.Errorf("Expected method 'App.start', got %v", extracted.Methods)
	}
	foundInstantiation := false
	for _, call := range extracted.Calls {
		if call.Kind == "instantiation" && call.Target == "UserService" {
			foundInstantiation = true
		}
	}
	if !foundInstantiation {
		t.Errorf("Expected instantiation of UserService, got %v", extracted.Calls)
	}
}
