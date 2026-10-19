// edit.go -- edit any resource the daemon accepts, without knowing in advance
// what its directives are.
//
// WHY THIS IS NOT A SECOND HARD-CODED FORM. The first editing form knew three
// directives by name: a Director's Name, Password and Monitor. That worked and
// it does not scale: a Client has 52 directives, a Director has 25, and a new
// Bareos version can add more. A form built from a list somebody typed needs a
// developer every time the list changes.
//
// The daemon already publishes the list. `bareos-fd --xs` prints its own
// configuration schema as JSON -- every resource type, every directive, its
// datatype, whether it is required, its default, whether it is deprecated. So
// the form is generated from what this binary on this machine says it accepts,
// and a directive nobody anticipated appears as a field with no code change.
//
// WHERE THE CURRENT VALUES COME FROM, AND WHY NOT FROM --xc. The rest of this
// program reads configuration through `bareos-fd --xc`, which reports what the
// daemon resolved -- the right answer for *showing* state. It is the wrong
// source for an editor, because --xc prints a password as its MD5 hash. Round
// -tripping that back into the file would store a hash of a hash and the
// director would stop being able to authenticate. The editor therefore reads
// the resource's own .conf file, verbatim, and writes back the same shape.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ------------------------------------------------------------------ schema

type schemaField struct {
	Datatype   string `json:"datatype"`
	Required   bool   `json:"required"`
	Deprecated bool   `json:"deprecated"`
	Alias      bool   `json:"alias"`
	Default    any    `json:"default_value"`
}

type bareosSchema struct {
	Version  string                                       `json:"version"`
	Resource map[string]map[string]map[string]schemaField `json:"resource"`
}

// loadSchema asks the daemon what it accepts. The answer is cached for a
// minute: it cannot change while the binary does not, and the page polls.
var (
	schemaCache   map[string]map[string]schemaField
	schemaVersion string
	schemaWhen    time.Time
)

func loadSchema(e Env) (map[string]map[string]schemaField, string, error) {
	if schemaCache != nil && time.Since(schemaWhen) < time.Minute {
		return schemaCache, schemaVersion, nil
	}
	if e.FdBin == "" {
		return nil, "", fmt.Errorf("no file daemon binary was found, so its " +
			"schema cannot be read")
	}
	out, err := runCapture(e.FdBin, []string{"--xs", "-c", e.ConfigDir}, 30*time.Second)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, "", fmt.Errorf("asking the daemon for its schema failed: %v", err)
	}
	// The daemon prints warnings before the JSON on some configurations, so
	// start at the first brace rather than assuming the output is pure JSON.
	i := strings.Index(out, "{")
	if i < 0 {
		return nil, "", fmt.Errorf("the daemon printed no JSON schema")
	}
	var s bareosSchema
	if err := json.Unmarshal([]byte(out[i:]), &s); err != nil {
		return nil, "", fmt.Errorf("the schema did not parse: %v", err)
	}
	for _, byType := range s.Resource {
		schemaCache, schemaVersion, schemaWhen = byType, s.Version, time.Now()
		return schemaCache, schemaVersion, nil
	}
	return nil, "", fmt.Errorf("the schema named no resources")
}

// ------------------------------------------------- the file a resource is in

var (
	blockRe = regexp.MustCompile(`(?s)^\s*([A-Za-z][A-Za-z ]*)\s*\{(.*)\}\s*$`)
	kvRe    = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9 _]*?)\s*=\s*(.*?)\s*$`)
)

type rawResource struct {
	Type  string
	Keys  map[string]string
	Order []string
	Path  string
}

// parseRaw reads one .conf file as it is written, not as the daemon resolved
// it. Quotes are kept off the value and put back on write.
func parseRaw(path string) (*rawResource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := blockRe.FindStringSubmatch(string(b))
	if m == nil {
		return nil, fmt.Errorf("%s does not hold exactly one resource block",
			filepath.Base(path))
	}
	r := &rawResource{
		Type: strings.TrimSpace(m[1]),
		Keys: map[string]string{},
		Path: path,
	}
	for _, line := range strings.Split(m[2], "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		kv := kvRe.FindStringSubmatch(t)
		if kv == nil {
			continue
		}
		key := strings.ReplaceAll(kv[1], " ", "")
		val := strings.TrimSuffix(strings.TrimPrefix(kv[2], `"`), `"`)
		if _, seen := r.Keys[key]; !seen {
			r.Order = append(r.Order, key)
		}
		r.Keys[key] = val
	}
	return r, nil
}

// findResource locates the file that defines one resource. A Client called
// "other-site-fd" commonly lives in myself.conf, so the file cannot be derived
// from the name -- every candidate is read and asked what it holds.
func findResource(e Env, typ, name string) (*rawResource, error) {
	dir := filepath.Join(e.ConfigDir, "bareos-fd.d", strings.ToLower(typ))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no %s directory under the configuration: %v", typ, err)
	}
	for _, en := range entries {
		if en.IsDir() || !strings.EqualFold(filepath.Ext(en.Name()), ".conf") {
			continue
		}
		r, err := parseRaw(filepath.Join(dir, en.Name()))
		if err != nil {
			continue
		}
		if strings.EqualFold(r.Keys["Name"], name) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("nothing in %s defines a %s called %q", dir, typ, name)
}

// ------------------------------------------------------------------ writing

// A value that is not a bare word has to be quoted, and a value that already
// contains a quote cannot be written at all without escaping rules this does
// not implement -- so it is refused rather than mangled.
var bareWordRe = regexp.MustCompile(`^[A-Za-z0-9_.:/-]+$`)

func renderResource(typ string, order []string, keys map[string]string) (string, error) {
	var sb strings.Builder
	sb.WriteString(typ + " {\n")
	for _, k := range order {
		v, ok := keys[k]
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if strings.ContainsAny(v, "\"\n\r") {
			return "", fmt.Errorf("the value for %s contains a quote or a "+
				"newline, which this editor will not write", k)
		}
		if bareWordRe.MatchString(v) {
			sb.WriteString("  " + k + " = " + v + "\n")
		} else {
			sb.WriteString("  " + k + " = \"" + v + "\"\n")
		}
	}
	sb.WriteString("}\n")
	return sb.String(), nil
}

// saveResource writes the resource and makes the daemon the referee. The
// previous bytes are held in memory and put back verbatim if the daemon
// refuses, so a rejected edit changes nothing at all.
func saveResource(e Env, r *rawResource, order []string, keys map[string]string) error {
	text, err := renderResource(r.Type, order, keys)
	if err != nil {
		return err
	}
	prev, err := os.ReadFile(r.Path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.Path, []byte(text), 0o600); err != nil {
		return err
	}
	if out, err := validate(e); err != nil {
		os.WriteFile(r.Path, prev, 0o600)
		return fmt.Errorf("the daemon refused this configuration, so %s was put "+
			"back exactly as it was:\n%s", filepath.Base(r.Path), out)
	}
	return nil
}

// ------------------------------------------------------------------- the UI

type editField struct {
	Name       string
	Value      string
	Datatype   string
	Required   bool
	Deprecated bool
	Set        bool
	Secret     bool
}

type editPage struct {
	Type     string
	Name     string
	File     string
	Version  string
	Fields   []editField
	Token    string
	Err      string
	Resource string
}

func isSecret(datatype string) bool {
	d := strings.ToUpper(datatype)
	return strings.Contains(d, "PASSWORD") || strings.Contains(d, "PASSPHRASE")
}

// buildEditPage pairs what the daemon says it accepts with what the file
// currently says. Directives already set come first, because those are the
// ones somebody came to change; the rest follow, in name order.
func buildEditPage(e Env, typ, name, token string) (*editPage, error) {
	schema, version, err := loadSchema(e)
	if err != nil {
		return nil, err
	}
	fields, ok := schema[typ]
	if !ok {
		return nil, fmt.Errorf("the daemon does not accept a resource called %q", typ)
	}
	r, err := findResource(e, typ, name)
	if err != nil {
		return nil, err
	}
	p := &editPage{Type: typ, Name: name, File: r.Path, Version: version, Token: token}

	seen := map[string]bool{}
	for _, k := range r.Order {
		f := fields[k]
		p.Fields = append(p.Fields, editField{
			Name: k, Value: r.Keys[k], Datatype: f.Datatype, Required: f.Required,
			Deprecated: f.Deprecated, Set: true, Secret: isSecret(f.Datatype),
		})
		seen[k] = true
	}
	var rest []string
	for k, f := range fields {
		// An alias is the same directive under another spelling; offering both
		// invites writing two names for one setting.
		if seen[k] || f.Alias {
			continue
		}
		rest = append(rest, k)
	}
	sort.Strings(rest)
	for _, k := range rest {
		f := fields[k]
		p.Fields = append(p.Fields, editField{
			Name: k, Datatype: f.Datatype, Required: f.Required,
			Deprecated: f.Deprecated, Secret: isSecret(f.Datatype),
		})
	}
	if b, err := os.ReadFile(r.Path); err == nil {
		p.Resource = string(b)
	}
	return p, nil
}
