package main

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Several downloads added together: every "Which version?" question comes
// first and nothing is added before the player has chosen. Each download's
// pick stays with that download, and a download without versions is added
// once, not dropped by the other downloads' picks.
func TestAddSeveralDownloadsWithVersions(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	base := t.TempDir()
	versions := func(name, file string) string {
		d := join(base, name)
		for _, v := range []string{"Red", "Blue"} {
			write(t, join(d, v, file+".utoc"), "toc")
			write(t, join(d, v, file+".ucas"), name+" "+v)
		}
		return d
	}
	jets := versions("Jets", "JetLivery_P")
	birds := versions("Birds", "BirdLivery_P")
	plain := join(base, "Plain")
	write(t, join(plain, "Plain_P.utoc"), "toc")
	write(t, join(plain, "Plain_P.ucas"), "plain")
	paths := []string{jets, birds, plain}

	res, err := addFromPaths(g, paths, nil)
	if err != nil || len(res.Added)+len(res.Updated) != 0 {
		t.Fatalf("something was added before choosing: %+v %v", res, err)
	}
	bySource := map[string][]string{}
	for _, c := range res.Choices {
		if !strings.HasPrefix(c.ID, c.Source+"|") {
			t.Fatalf("choice id %q is not scoped to %q", c.ID, c.Source)
		}
		bySource[c.Source] = append(bySource[c.Source], c.Name)
	}
	if len(bySource["Jets"]) != 2 || len(bySource["Birds"]) != 2 || len(bySource) != 2 {
		t.Fatalf("choices: %+v", res.Choices)
	}
	if mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir()); len(mods) != 0 {
		t.Fatalf("mods were added before choosing: %v", keys(mods))
	}

	pick := func(source, name string) string {
		for _, c := range res.Choices {
			if c.Source == source && c.Name == name {
				return c.ID
			}
		}
		t.Fatalf("no choice %s in %s", name, source)
		return ""
	}
	res, err = addFromPaths(g, paths, []string{pick("Jets", "Red"), pick("Birds", "Blue")})
	if err != nil || len(res.Choices) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	added := append([]string(nil), res.Added...)
	sort.Strings(added)
	if want := []string{"Birds - Blue", "Jets - Red", "Plain"}; !reflect.DeepEqual(added, want) || len(res.Updated) != 0 {
		t.Fatalf("added %v, updated %v; want %v added once each", res.Added, res.Updated, want)
	}
	for mod, want := range map[string]string{"Jets - Red/JetLivery_P.ucas": "Jets Red", "Birds - Blue/BirdLivery_P.ucas": "Birds Blue"} {
		if b, _ := os.ReadFile(join(g.loaderDir(), mod)); string(b) != want {
			t.Fatalf("%s holds %q, want %q", mod, b, want)
		}
	}
}

// Without a file chooser (Linux), the mod's path is typed or pasted: quotes
// around it and "~/" for the home folder work, as for the other typed folders.
func TestAddTypedPath(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, join(home, "Downloads", "Typed", "Typed_P.utoc"), "toc")
	write(t, join(home, "Downloads", "Typed", "Typed_P.ucas"), "cas")
	res, err := addFromPaths(g, []string{` "~/Downloads/Typed" `}, nil)
	if err != nil || !reflect.DeepEqual(res.Added, []string{"Typed"}) {
		t.Fatalf("%+v %v", res, err)
	}
}

// The support report lists why files didn't load, not only the summary.
func TestSupportReportListsProblems(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	b, err := os.ReadFile("testdata/AC8IoStoreLoader-1.3.1-paks.log")
	if err != nil {
		t.Fatal(err)
	}
	write(t, join(g.loaderDir(), "AC8IoStoreLoader.log"), string(b))
	st := buildState()
	if st.Run == nil || len(st.Run.Problems) == 0 {
		t.Fatalf("the log's problems were not read: %+v", st.Run)
	}
	r := supportReport()
	for _, p := range st.Run.Problems {
		if !strings.Contains(r, "  Mod Loader: "+p) {
			t.Fatalf("the report doesn't list the problem %q", p)
		}
	}
}
