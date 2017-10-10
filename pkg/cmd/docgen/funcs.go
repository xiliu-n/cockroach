// Copyright 2017 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	markdown "github.com/golang-commonmark/markdown"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/cockroachdb/cockroach/pkg/sql/parser"
)

var functionsCmd = &cobra.Command{
	Use:   "functions <output-dir>",
	Short: "generate markdown documentation of functions and operators",
	RunE: func(cmd *cobra.Command, args []string) error {
		outDir := filepath.Join("docs", "generated", "sql")
		if len(args) > 0 {
			outDir = args[0]
		}

		if stat, err := os.Stat(outDir); err != nil || !stat.IsDir() {
			return errors.Errorf("%s does not exist", outDir)
		}

		if err := ioutil.WriteFile(
			filepath.Join(outDir, "functions.md"), generateFunctions(parser.Builtins, true), 0644,
		); err != nil {
			return err
		}
		if err := ioutil.WriteFile(
			filepath.Join(outDir, "aggregates.md"), generateFunctions(parser.Aggregates, false), 0644,
		); err != nil {
			return err
		}
		if err := ioutil.WriteFile(
			filepath.Join(outDir, "operators.md"), generateOperators(), 0644,
		); err != nil {
			return err
		}

		return nil
	},
}

type operation struct {
	left  string
	right string
	ret   string
	op    string
}

func (o operation) String() string {
	if o.right == "" {
		return fmt.Sprintf("<code>%s</code>%s", o.op, linkTypeName(o.left))
	}
	return fmt.Sprintf("%s <code>%s</code> %s", linkTypeName(o.left), o.op, linkTypeName(o.right))
}

type operations []operation

func (p operations) Len() int      { return len(p) }
func (p operations) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p operations) Less(i, j int) bool {
	if p[i].right != "" && p[j].right == "" {
		return false
	}
	if p[i].right == "" && p[j].right != "" {
		return true
	}
	if p[i].left != p[j].left {
		return p[i].left < p[j].left
	}
	if p[i].right != p[j].right {
		return p[i].right < p[j].right
	}
	return p[i].ret < p[j].ret
}

func generateOperators() []byte {
	ops := make(map[string]operations)
	for optyp, overloads := range parser.UnaryOps {
		op := optyp.String()
		for _, untyped := range overloads {
			v := untyped.(parser.UnaryOp)
			ops[op] = append(ops[op], operation{
				left: v.Typ.String(),
				ret:  v.ReturnType.String(),
				op:   op,
			})
		}
	}
	for optyp, overloads := range parser.BinOps {
		op := optyp.String()
		for _, untyped := range overloads {
			v := untyped.(parser.BinOp)
			left := v.LeftType.String()
			right := v.RightType.String()
			ops[op] = append(ops[op], operation{
				left:  left,
				right: right,
				ret:   v.ReturnType.String(),
				op:    op,
			})
		}
	}
	for optyp, overloads := range parser.CmpOps {
		op := optyp.String()
		for _, untyped := range overloads {
			v := untyped.(parser.CmpOp)
			left := v.LeftType.String()
			right := v.RightType.String()
			ops[op] = append(ops[op], operation{
				left:  left,
				right: right,
				ret:   "bool",
				op:    op,
			})
		}
	}
	var opstrs []string
	for k, v := range ops {
		sort.Sort(v)
		opstrs = append(opstrs, k)
	}
	sort.Strings(opstrs)
	b := new(bytes.Buffer)
	for _, op := range opstrs {
		fmt.Fprintf(b, "<table><thead>\n")
		fmt.Fprintf(b, "<tr><td><code>%s</code></td><td>Return</td></tr>\n", op)
		fmt.Fprintf(b, "</thead><tbody>\n")
		for _, v := range ops[op] {
			fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td></tr>\n", v.String(), linkTypeName(v.ret))
		}
		fmt.Fprintf(b, "</tbody></table>")
		fmt.Fprintln(b)
	}
	return b.Bytes()
}

// TODO(mjibson): use the exported value from sql/parser/pg_builtins.go.
const notUsableInfo = "Not usable; exposed only for compatibility with PostgreSQL."

func generateFunctions(from map[string][]parser.Builtin, categorize bool) []byte {
	functions := make(map[string][]string)
	seen := make(map[string]struct{})
	md := markdown.New(markdown.XHTMLOutput(true), markdown.Nofollow(true))
	for name, fns := range from {
		// NB: funcs can appear more than once i.e. upper/lowercase varients for
		// faster lookups, so normalize to lowercase and de-dupe using a set.
		name = strings.ToLower(name)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		for _, fn := range fns {
			if fn.Info == notUsableInfo {
				continue
			}
			if categorize && fn.WindowFunc != nil {
				continue
			}
			args := fn.Types.String()
			ret := fn.FixedReturnType().String()
			cat := ret
			if c := fn.Category(); c != "" {
				cat = c
			}
			if !categorize {
				cat = ""
			}
			extra := ""
			if fn.Info != "" {
				// Render the info field to HTML upfront, because Markdown
				// won't do it automatically in a table context.
				// Boo Markdown, bad Markdown.
				// TODO(knz): Do not use Markdown.
				info := md.RenderToString([]byte(fn.Info))
				extra = fmt.Sprintf("<span class=\"funcdesc\">%s</span>", info)
			}
			s := fmt.Sprintf("<tr><td><code>%s(%s) &rarr; %s</code></td><td>%s</td></tr>", name, linkArguments(args), linkArguments(ret), extra)
			functions[cat] = append(functions[cat], s)
		}
	}
	var cats []string
	for k, v := range functions {
		sort.Strings(v)
		cats = append(cats, k)
	}
	sort.Strings(cats)
	// HACK: swap "Compatibility" to be last.
	// TODO(dt): Break up generated list be one _include per category, to allow
	// manually written copy on some sections.
	for i, cat := range cats {
		if cat == "Compatibility" {
			cats = append(append(cats[:i], cats[i+1:]...), "Compatibility")
			break
		}
	}
	b := new(bytes.Buffer)
	for _, cat := range cats {
		if categorize {
			fmt.Fprintf(b, "### %s Functions\n\n", cat)
		}
		b.WriteString("<table>\n<thead><tr><th>Function &rarr; Returns</th><th>Description</th></tr></thead>\n")
		b.WriteString("<tbody>\n")
		b.WriteString(strings.Join(functions[cat], "\n"))
		b.WriteString("</tbody>\n</table>\n\n")
	}
	return b.Bytes()
}

var linkRE = regexp.MustCompile(`([a-z]+)([\.\[\]]*)$`)

func linkArguments(t string) string {
	sp := strings.Split(t, ", ")
	for i, s := range sp {
		sp[i] = linkRE.ReplaceAllStringFunc(s, func(s string) string {
			match := linkRE.FindStringSubmatch(s)
			s = linkTypeName(match[1])
			return s + match[2]
		})
	}
	return strings.Join(sp, ", ")
}

func linkTypeName(s string) string {
	s = strings.TrimSuffix(s, "{}")
	name := s
	switch s {
	case "timestamptz":
		s = "timestamp"
	}
	s = strings.TrimSuffix(s, "[]")
	switch s {
	case "int", "decimal", "float", "bool", "date", "timestamp", "interval", "string", "bytes",
		"inet", "uuid", "collatedstring":
		s = fmt.Sprintf("<a href=\"%s.html\">%s</a>", s, name)
	}
	return s
}
