package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func TestExecutionCalculationCollectionRoutePrecedesIDRoute(t *testing.T) {
	source, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "routes.go", source, 0)
	if err != nil {
		t.Fatalf("parse routes: %v", err)
	}
	collectionIndex, byIDIndex := -1, -1
	position := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Get" || len(call.Args) < 2 {
			return true
		}
		path, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			return true
		}
		handler, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch {
		case path.Value == `"/:uuid/smart-targeting/execution-audience-calculations"` && handler.Sel.Name == "GetCurrentSmartTargetingExecutionCalculation":
			collectionIndex = position
			position++
		case path.Value == `"/:uuid/smart-targeting/execution-audience-calculations/:calculation_id"` && handler.Sel.Name == "GetSmartTargetingExecutionCalculation":
			byIDIndex = position
			position++
		}
		return true
	})
	if collectionIndex < 0 || byIDIndex < 0 {
		t.Fatalf("execution calculation polling routes are missing")
	}
	if collectionIndex > byIDIndex {
		t.Fatal("collection route must be registered before the by-ID route")
	}
}
