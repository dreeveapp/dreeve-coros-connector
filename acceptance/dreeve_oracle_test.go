package acceptance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type dreevePin struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
}

type fitToolMessage struct {
	Name   string `json:"name"`
	Fields []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	} `json:"fields"`
}

type fitToolOutput struct {
	Files []struct {
		ProfileVersion json.Number      `json:"profileVersion"`
		Messages       []fitToolMessage `json:"messages"`
	} `json:"files"`
}

func TestDreeveOracleReadsTheCOROSTitleAndEveryOriginalMessage(t *testing.T) {
	if os.Getenv("DREEVE_ORACLE") != "1" {
		t.Skip("set DREEVE_ORACLE=1 to build Dreeve's fit-tool and decode delivered files with it")
	}
	fitTool := buildDreeveFitTool(t)
	w := newWorld(t, 1, 1)
	golden, err := filepath.Glob("../testdata/fit/*.fit")
	if err != nil || len(golden) == 0 {
		t.Fatalf("no golden FIT files: %v", err)
	}
	labelIDs := map[string]string{}
	for i, path := range golden {
		a := w.addActivity(labelID(i), i+1, withContent(goldenFIT(t, filepath.Base(path))), withName(oracleTitle(path)))
		labelIDs[path] = a.LabelID
	}
	w.mustRun("login")
	w.mustRun("sync-once")

	for path, id := range labelIDs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			delivered := filepath.Join(w.watch, "coros_"+id+".fit")
			got := runFitTool(t, fitTool, delivered)
			want := runFitTool(t, fitTool, path)

			title := oracleTitle(path)
			if names := fitToolWorkoutNames(got); len(names) != 1 || names[0] != title {
				t.Fatalf("fit-tool wkt_name = %q, want [%q]", names, title)
			}
			if !reflect.DeepEqual(withoutFitToolWorkouts(got), want) {
				t.Fatal("fit-tool reports different original messages for the delivered file")
			}
		})
	}
}

func oracleTitle(path string) string {
	return "Activity " + filepath.Base(path) + " · Zone 2 ☀️"
}

func buildDreeveFitTool(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../testdata/dreeve/fit-tool.json")
	if err != nil {
		t.Fatal(err)
	}
	var pin dreevePin
	if err := json.Unmarshal(data, &pin); err != nil {
		t.Fatalf("testdata/dreeve/fit-tool.json: %v", err)
	}
	src := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", pin.Repository},
		{"sparse-checkout", "set", pin.Path},
		{"fetch", "--quiet", "--depth", "1", "--filter", "blob:none", "origin", pin.Commit},
		{"checkout", "--quiet", "FETCH_HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	bin := filepath.Join(t.TempDir(), "fit-tool")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = filepath.Join(src, pin.Path)
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Dreeve fit-tool at %s: %v\n%s", pin.Commit, err, out)
	}
	return bin
}

func runFitTool(t *testing.T, fitTool, path string) fitToolOutput {
	t.Helper()
	cmd := exec.Command(fitTool, path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("fit-tool %s: %v\n%s", filepath.Base(path), err, stderr.String())
	}
	if strings.Contains(stderr.String(), "warning") {
		t.Fatalf("fit-tool %s warned: %s", filepath.Base(path), stderr.String())
	}
	var out fitToolOutput
	dec := json.NewDecoder(&stdout)
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("fit-tool %s output is not JSON: %v", filepath.Base(path), err)
	}
	return out
}

func fitToolWorkoutNames(out fitToolOutput) []string {
	var names []string
	for _, f := range out.Files {
		for _, m := range f.Messages {
			if m.Name != "workout" {
				continue
			}
			for _, field := range m.Fields {
				if s, ok := field.Value.(string); ok && field.Name == "wkt_name" {
					names = append(names, s)
				}
			}
		}
	}
	return names
}

func withoutFitToolWorkouts(out fitToolOutput) fitToolOutput {
	for i, f := range out.Files {
		kept := f.Messages[:0:0]
		for _, m := range f.Messages {
			if m.Name != "workout" {
				kept = append(kept, m)
			}
		}
		out.Files[i].Messages = kept
	}
	return out
}
