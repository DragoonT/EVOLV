package agent

import (
	"strings"
	"testing"
)

// TestPluginPermissionDenied: a plugin without perms throws a precise
// permission error instead of reaching the capability.
func TestPluginPermissionDenied(t *testing.T) {
	a := testAgent(t)
	p, err := a.js.buildPluginVM("locked", `
		evolv.lastErr = "";
		try { evolv.http("https://example.com"); } catch (e) { evolv.lastErr = String(e); }
		try { evolv.workspace_dir(); } catch (e) { evolv.lastErr += "|" + String(e); }
		try { evolv.toast("hi"); } catch (e) { evolv.lastErr += "|" + String(e); }
		evolv.ok = true; // code after the catches still runs
	`, nil)
	if err != nil {
		t.Fatalf("plugin build failed: %v", err)
	}
	got := func(expr string) string {
		v, _ := p.vm.RunString(expr)
		return v.String()
	}
	if !strings.Contains(got("evolv.lastErr"), `plugin "locked" lacks permission "http"`) {
		t.Fatalf("http not denied: %q", got("evolv.lastErr"))
	}
	if !strings.Contains(got("evolv.lastErr"), `lacks permission "fs"`) {
		t.Fatalf("fs not denied: %q", got("evolv.lastErr"))
	}
	if !strings.Contains(got("evolv.lastErr"), `lacks permission "toast"`) {
		t.Fatalf("toast not denied: %q", got("evolv.lastErr"))
	}
	if got("evolv.ok") != "true" {
		t.Fatal("denial must be catchable, not fatal")
	}
	// the deny message teaches the fix
	if !strings.Contains(got("evolv.lastErr"), "re-evolve it with perms") {
		t.Fatalf("deny message should teach the fix: %q", got("evolv.lastErr"))
	}
}

// TestPluginPermissionGranted: with perms the capabilities work.
func TestPluginPermissionGranted(t *testing.T) {
	a := testAgent(t)
	p, err := a.js.buildPluginVM("rooted", "evolv.dir = evolv.workspace_dir();", []string{"fs"})
	if err != nil {
		t.Fatalf("plugin build failed: %v", err)
	}
	v, _ := p.vm.RunString("evolv.dir")
	if v.String() != a.ws.Root {
		t.Fatalf("fs perm not granted: %q != %q", v.String(), a.ws.Root)
	}
}

// TestPluginPermsPersistedAndReloaded: evolve_plugin persists perms in
// plugin.json and reload reads them back.
func TestPluginPermsPersistedAndReloaded(t *testing.T) {
	a := testAgent(t)
	res := execOK(t, a, action{Action: "evolve_plugin", Name: "netwatch", Desc: "watches the web",
		Code:  "evolv.on('evolution', function(){});",
		Perms: []string{"http", "fs"}})
	if res["ok"] != true {
		t.Fatalf("evolve_plugin failed: %v", res)
	}
	perms := pluginPermsFromManifest(a, "netwatch")
	if len(perms) != 2 || perms[0] != "http" || perms[1] != "fs" {
		t.Fatalf("perms not persisted: %v", perms)
	}
	// an un-permed plugin reads back empty
	if got := pluginPermsFromManifest(a, "nope"); len(got) != 0 {
		t.Fatalf("missing plugin should have no perms: %v", got)
	}
}

// TestPluginCoreAlwaysAllowed: log/on/every are core — no perm needed.
func TestPluginCoreAlwaysAllowed(t *testing.T) {
	a := testAgent(t)
	_, err := a.js.buildPluginVM("core", `
		evolv.log("hello");
		evolv.on("evolution", function(){});
		evolv.every(60, function(){});
		evolv.coreOK = true;
	`, nil)
	if err != nil {
		t.Fatalf("core capabilities must not need perms: %v", err)
	}
}
