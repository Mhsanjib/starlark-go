package stargo_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"testing"

	"go.starlark.net/internal/chunkedfile"
	"go.starlark.net/resolve"
	"go.starlark.net/stargo"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarktest"
)

func init() {
	// The tests make extensive use of these not-yet-standard features.
	resolve.AllowLambda = true
	resolve.AllowNestedDef = true
	resolve.AllowFloat = true
	resolve.AllowSet = true
	resolve.AllowBitwise = true
	resolve.AllowGlobalReassign = true
}

func TestExecFile(t *testing.T) {
	testdata := starlarktest.DataFile("stargo", ".")
	thread := &starlark.Thread{Load: load}
	starlarktest.SetReporter(thread, t)
	for _, file := range []string{
		"testdata/bytes.star",
		"testdata/chan.star",
		"testdata/complex.star",
		"testdata/http.star",
		"testdata/int.star",
		"testdata/map.star",
		"testdata/parser.star",
		"testdata/slice.star",
	} {
		filename := filepath.Join(testdata, file)
		for _, chunk := range chunkedfile.Read(filename, t) {

			predeclared := starlark.StringDict{
				"go": stargo.Builtins,
			}

			_, err := starlark.ExecFile(thread, filename, chunk.Source, predeclared)
			switch err := err.(type) {
			case *starlark.EvalError:
				found := false
				for _, fr := range err.Stack() {
					posn := fr.Position()
					if posn.Filename() == filename {
						chunk.GotError(int(posn.Line), err.Error())
						found = true
						break
					}
				}
				if !found {
					t.Error(err.Backtrace())
				}
			case nil:
				// success
			default:
				t.Error(err)
			}
			chunk.Done()
		}
	}
}

// load implements the 'load' operation as used in the evaluator tests.
func load(thread *starlark.Thread, module string) (starlark.StringDict, error) {
	switch module {
	case "assert.star":
		return starlarktest.LoadAssertModule()
	case "go":
		return goPackages, nil
	}
	return nil, fmt.Errorf("no such module")
}

// Some typical Go packages for testing.
var goPackages = starlark.StringDict{
	"fmt": &stargo.Package{
		Path: "fmt",
		Name: "fmt",
		Members: starlark.StringDict{
			"Errorf":   stargo.ValueOf(fmt.Errorf),
			"Fprintf":  stargo.ValueOf(fmt.Fprintf),
			"Stringer": stargo.TypeOf(new(fmt.Stringer)),
		},
	},
	"bytes": &stargo.Package{
		Path: "bytes",
		Name: "bytes",
		Members: starlark.StringDict{
			"Buffer":      stargo.TypeOf(new(bytes.Buffer)),
			"ErrTooLarge": stargo.VarOf(&bytes.ErrTooLarge),
			"Split":       stargo.ValueOf(bytes.Split),
		},
	},
	"io/ioutil": &stargo.Package{
		Path: "io/ioutil",
		Name: "ioutil",
		Members: starlark.StringDict{
			"ReadAll": stargo.ValueOf(ioutil.ReadAll),
		},
	},
	"net/http": &stargo.Package{
		Path: "net/http",
		Name: "http",
		Members: starlark.StringDict{
			"Get":    stargo.ValueOf(http.Get),
			"Header": stargo.TypeOf(new(http.Header)),
		},
	},
	"encoding/json": &stargo.Package{
		Path: "encoding/json",
		Name: "json",
		Members: starlark.StringDict{
			"MarshalIndent": stargo.ValueOf(json.MarshalIndent),
		},
	},
	"stargo_test": &stargo.Package{
		Path: "stargo_test",
		Name: "json",
		Members: starlark.StringDict{
			"myint16": stargo.TypeOf(new(myint16)),
		},
	},
	"go/token": &stargo.Package{
		Path: "go/token",
		Name: "token",
		Members: starlark.StringDict{
			"FileSet":    stargo.TypeOf(new(token.FileSet)),
			"NewFileSet": stargo.ValueOf(token.NewFileSet),
			"Pos":        stargo.TypeOf(new(token.Pos)),
		},
	},
	"go/parser": &stargo.Package{
		Path: "go/parser",
		Name: "parser",
		Members: starlark.StringDict{
			"Mode":              stargo.TypeOf(new(parser.Mode)),
			"PackageClauseOnly": stargo.ValueOf(parser.PackageClauseOnly),
			"ParseFile":         stargo.ValueOf(parser.ParseFile),
		},
	},
}

type myint16 int16

func (i myint16) Get() int { return int(i) }
func (i *myint16) Incr()   { *i++ }
