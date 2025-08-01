// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package shadow

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/analysis/passes/internal/analysisutil"
	"golang.org/x/tools/go/ast/inspector"
)

// NOTE: Experimental. Not part of the vet suite.

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "shadow",
	Doc:      analysisutil.MustExtractDoc(doc, "shadow"),
	URL:      "https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/shadow",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// flags
var strict = false

func init() {
	Analyzer.Flags.BoolVar(&strict, "strict", strict, "whether to be strict about shadowing; can be noisy")
}

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	analyzer := &shadowAnalyzer{
		pass:           pass,
		inspect:        inspect,
		usagesByObject: make(map[types.Object][]*ast.Ident),
		assignStmts:    make([]*ast.AssignStmt, 0),
		incDecStmts:    make([]*ast.IncDecStmt, 0),
	}

	for ident, obj := range pass.TypesInfo.Uses {
		if obj != nil {
			analyzer.usagesByObject[obj] = append(analyzer.usagesByObject[obj], ident)
		}
	}

	nodeFilter := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.IncDecStmt)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.AssignStmt:
			analyzer.assignStmts = append(analyzer.assignStmts, n)
		case *ast.IncDecStmt:
			analyzer.incDecStmts = append(analyzer.incDecStmts, n)
		}
	})

	declFilter := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.GenDecl)(nil),
	}
	inspect.Preorder(declFilter, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.AssignStmt:
			analyzer.checkShadowAssignment(n)
		case *ast.GenDecl:
			analyzer.checkShadowDecl(n)
		}
	})
	return nil, nil
}

type shadowAnalyzer struct {
	pass           *analysis.Pass
	inspect        *inspector.Inspector
	usagesByObject map[types.Object][]*ast.Ident
	assignStmts    []*ast.AssignStmt
	incDecStmts    []*ast.IncDecStmt
}

// checkShadowAssignment checks for shadowing in a short variable declaration.
func (sa *shadowAnalyzer) checkShadowAssignment(a *ast.AssignStmt) {
	if a.Tok != token.DEFINE {
		return
	}
	if idiomaticShortRedecl(sa.pass, a) || loopVariableDecl(sa.pass, a) {
		return
	}
	for _, expr := range a.Lhs {
		ident, ok := expr.(*ast.Ident)
		if !ok {
			sa.pass.ReportRangef(expr, "invalid AST: short variable declaration of non-identifier")
			return
		}
		sa.checkShadowing(ident)
	}
}

// loopVariableDecl checks if this assignment statement is a loop variable declaration.
func loopVariableDecl(pass *analysis.Pass, a *ast.AssignStmt) bool {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	var isLoop bool
	inspect.Preorder([]ast.Node{(*ast.ForStmt)(nil), (*ast.RangeStmt)(nil)}, func(n ast.Node) {
		if isLoop {
			return
		}
		switch stmt := n.(type) {
		case *ast.ForStmt:
			isLoop = (stmt.Init == a)
		case *ast.RangeStmt:
			isLoop = stmt.Tok == token.DEFINE && isRangeVariableMatch(a, stmt)
		}
	})
	return isLoop
}

func isRangeVariableMatch(a *ast.AssignStmt, stmt *ast.RangeStmt) bool {
	for _, lhs := range a.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}
		for _, rangeVar := range []ast.Expr{stmt.Key, stmt.Value} {
			if rangeVar == nil {
				continue
			}
			if rangeIdent, ok := rangeVar.(*ast.Ident); ok && rangeIdent.Pos() == ident.Pos() {
				return true
			}
		}
	}
	return false
}

// idiomaticShortRedecl reports whether this short declaration can be ignored for
// the purposes of shadowing, that is, that any redeclarations it contains are deliberate.
func idiomaticShortRedecl(pass *analysis.Pass, a *ast.AssignStmt) bool {
	// Don't complain about deliberate redeclarations of the form
	//	i := i
	// Such constructs are idiomatic in range loops to create a new variable
	// for each iteration. Another example is
	//	switch n := n.(type)
	if len(a.Rhs) != len(a.Lhs) {
		return false
	}
	// We know it's an assignment, so the LHS must be all identifiers. (We check anyway.)
	for i, expr := range a.Lhs {
		lhs, ok := expr.(*ast.Ident)
		if !ok {
			pass.ReportRangef(expr, "invalid AST: short variable declaration of non-identifier")
			return true // Don't do any more processing.
		}
		switch rhs := a.Rhs[i].(type) {
		case *ast.Ident:
			if lhs.Name != rhs.Name {
				return false
			}
		case *ast.TypeAssertExpr:
			if id, ok := rhs.X.(*ast.Ident); ok {
				if lhs.Name != id.Name {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// idiomaticRedecl reports whether this declaration spec can be ignored for
// the purposes of shadowing, that is, that any redeclarations it contains are deliberate.
func idiomaticRedecl(d *ast.ValueSpec) bool {
	// Don't complain about deliberate redeclarations of the form
	//	var i, j = i, j
	// Don't ignore redeclarations of the form
	//	var i = 3
	if len(d.Names) != len(d.Values) {
		return false
	}
	for i, lhs := range d.Names {
		rhs, ok := d.Values[i].(*ast.Ident)
		if !ok || lhs.Name != rhs.Name {
			return false
		}
	}
	return true
}

// checkShadowDecl checks for shadowing in a general variable declaration.
func (sa *shadowAnalyzer) checkShadowDecl(d *ast.GenDecl) {
	if d.Tok != token.VAR {
		return
	}
	for _, spec := range d.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			sa.pass.ReportRangef(spec, "invalid AST: var GenDecl not ValueSpec")
			return
		}
		// Don't complain about deliberate redeclarations of the form
		//	var i = i
		if idiomaticRedecl(valueSpec) {
			return
		}
		for _, ident := range valueSpec.Names {
			sa.checkShadowing(ident)
		}
	}
}

// checkShadowing checks whether the identifier shadows an identifier in an outer scope.
func (sa *shadowAnalyzer) checkShadowing(ident *ast.Ident) {
	if ident.Name == "_" {
		// Can't shadow the blank identifier.
		return
	}
	obj := sa.pass.TypesInfo.Defs[ident]
	if obj == nil {
		return
	}
	// obj.Parent.Parent is the surrounding scope. If we can find another declaration
	// starting from there, we have a shadowed identifier.
	_, shadowed := obj.Parent().Parent().LookupParent(obj.Name(), obj.Pos())
	if shadowed == nil {
		return
	}
	// Don't complain if it's shadowing a universe-declared identifier; that's fine.
	if shadowed.Parent() == types.Universe {
		return
	}
	// Don't complain if the types differ: that implies the programmer really wants two different things.
	if !types.Identical(obj.Type(), shadowed.Type()) {
		return
	}
	if sa.allInnerAssignmentsUsed(obj, ident.Pos(), sa.usagesByObject[obj]) {
		return
	}
	if !sa.outerUsedAfterInner(shadowed, ident.Pos()) {
		return
	}
	shadowedPos := sa.pass.Fset.Position(shadowed.Pos())
	message := fmt.Sprintf("declaration of %q shadows declaration at line %d", obj.Name(), shadowedPos.Line)
	currentFile := sa.pass.Fset.Position(ident.Pos()).Filename
	if shadowedPos.Filename != currentFile {
		message += fmt.Sprintf(" in %s", filepath.Base(shadowedPos.Filename))
	}
	sa.pass.Report(analysis.Diagnostic{
		Pos:     ident.Pos(),
		End:     ident.End(),
		Message: message,
		Related: []analysis.RelatedInformation{{
			Pos:     shadowed.Pos(),
			End:     shadowed.Pos() + token.Pos(len(shadowed.Name())),
			Message: fmt.Sprintf("shadowed symbol %q declared here", obj.Name()),
		}},
	})
}

func (sa *shadowAnalyzer) allInnerAssignmentsUsed(obj types.Object, declPos token.Pos, usages []*ast.Ident) bool {
	for _, stmt := range sa.assignStmts {
		for _, lhs := range stmt.Lhs {
			if ident, ok := lhs.(*ast.Ident); ok {
				if hasUnusedAssignment(ident, stmt, obj, declPos, sa.pass, usages) {
					return false
				}
			}
		}
	}
	for _, stmt := range sa.incDecStmts {
		if ident, ok := stmt.X.(*ast.Ident); ok {
			if hasUnusedAssignment(ident, stmt, obj, declPos, sa.pass, usages) {
				return false
			}
		}
	}
	return true
}

func hasUnusedAssignment(ident *ast.Ident, stmt ast.Node, obj types.Object, declPos token.Pos, pass *analysis.Pass, usages []*ast.Ident) bool {
	if pass.TypesInfo.Uses[ident] != obj || ident.Pos() <= declPos {
		return false
	}
	assignPos := ident.Pos()
	for _, ident := range usages {
		usePos := ident.Pos()
		if usePos > assignPos && usePos > declPos {
			if stmt != nil && usePos >= stmt.Pos() && usePos < stmt.End() {
				continue
			}
			return false
		}
	}
	return true
}

func (sa *shadowAnalyzer) outerUsedAfterInner(outerObj types.Object, innerDeclPos token.Pos) bool {
	declLine := sa.pass.Fset.Position(innerDeclPos).Line
	for _, outerIdent := range sa.usagesByObject[outerObj] {
		if outerIdent.Pos() > innerDeclPos && sa.pass.Fset.Position(outerIdent.Pos()).Line != declLine {
			return true
		}
	}
	return false
}
