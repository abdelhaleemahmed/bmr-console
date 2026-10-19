// serve.go -- the same five answers, in a browser, plus the two things a
// terminal cannot do well: edit a configuration safely and watch something
// change.
//
// WHY A BROWSER AND STILL NO GUI TOOLKIT. The argument in main.go has not
// moved: a Go GUI toolkit draws through OpenGL, which a WinPE and a
// framebuffer rescue console do not have. A browser is not a toolkit. The
// program serves HTML over net/http -- both standard library -- and the
// browser can be on the laptop next to the machine being recovered, which is
// usually where the person is anyway. Nothing is linked in, nothing is
// fetched from the internet, and the binary stays one static file.
//
// WHY EDITING IS A FORM AND NOT A TEXT BOX. The failure this is meant to
// prevent is a configuration that parses but is wrong, or does not parse at
// all on a machine with no editor you like and no second computer. So the
// form writes the resource, asks the daemon to read it back with -t, and
// puts the file back exactly as it was if the daemon refuses. You cannot
// leave this program with a configuration the daemon will not load.
//
// WHAT IT CAN AND CANNOT SEE OF A RESTORE. A file daemon reports progress to
// its director, not to the machine it runs on, so there is no local counter
// to read. What is locally true is: whether the daemon is listening, who is
// connected to it right now, and whether the target filesystem is filling up.
// The progress panel shows those three and says plainly that they are
// indirect. It does not invent a percentage.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- the model

type uiCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	// Meaning is the plain-language half: what this failure will look like
	// from the director's side, which is almost never what it looks like here.
	Meaning string `json:"meaning,omitempty"`
}

type uiResource struct {
	Type  string            `json:"type"`
	Name  string            `json:"name"`
	Pairs map[string]string `json:"pairs"`
	Order []string          `json:"order"`
}

type uiVolume struct {
	Path      string  `json:"path"`
	FreeBytes uint64  `json:"free_bytes"`
	Free      string  `json:"free"`
	RateMBs   float64 `json:"rate_mbs"`
	Filling   bool    `json:"filling"`
}

type uiState struct {
	Host      string       `json:"host"`
	OS        string       `json:"os"`
	Version   string       `json:"version"`
	Now       string       `json:"now"`
	Env       []uiCheck    `json:"env"`
	Port      int          `json:"port"`
	Checks    []uiCheck    `json:"checks"`
	Verdict   string       `json:"verdict"`
	VerdictOK bool         `json:"verdict_ok"`
	Resources []uiResource `json:"resources"`
	ConfigErr string       `json:"config_err,omitempty"`
	Peers     []string     `json:"peers"`
	PeersNote string       `json:"peers_note,omitempty"`
	Volumes   []uiVolume   `json:"volumes"`
}

// The diagnosis table. Each entry turns one observable failure into the
// sentence somebody can act on -- deliberately phrased as what the OTHER end
// will report, because that is the message people actually see first.
func meaningFor(c check) string {
	switch {
	case strings.HasPrefix(c.name, "daemon on") && !c.ok:
		return "Nothing is listening. Either the daemon never started, or it " +
			"started and exited on a configuration error. Run the daemon with " +
			"-t to make it check its own configuration and say why."
	case strings.HasPrefix(c.name, "link-local") && !c.ok:
		return "The static address was refused -- almost always another machine " +
			"holding it -- and the system fell back to an address nothing can " +
			"route to. The network looks configured and the client is unreachable."
	case c.name == "routable address" && !c.ok:
		return "This machine has no address a director could connect to at all. " +
			"Fix the network before looking at Bareos."
	case strings.HasPrefix(c.name, "on ") && !c.ok:
		return "The daemon is up but not reachable on this address. A firewall " +
			"or a bind to one interface will do this; the director will report " +
			"a connection timeout, which reads like the client being switched off."
	}
	return ""
}

// ------------------------------------------------------------- disk sampling

type sampler struct {
	mu   sync.Mutex
	last map[string]uint64
	when map[string]time.Time
	rate map[string]float64
}

func newSampler() *sampler {
	return &sampler{
		last: map[string]uint64{},
		when: map[string]time.Time{},
		rate: map[string]float64{},
	}
}

// observe records free space and returns the rate at which it is being
// consumed, in MB/s. A restore shows up here as a steady negative change in
// free space, which is the only locally visible sign that bytes are arriving.
func (s *sampler) observe(path string, free uint64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	prev, ok := s.last[path]
	prevT := s.when[path]
	s.last[path] = free
	s.when[path] = now
	if !ok || now.Sub(prevT) < 2*time.Second {
		return s.rate[path]
	}
	// Free space falling means data landing. Report it as a positive rate.
	delta := float64(prev) - float64(free)
	r := delta / now.Sub(prevT).Seconds() / (1024 * 1024)
	if r < 0 {
		r = 0
	}
	s.rate[path] = r
	return r
}

func humanBytes(n uint64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(u), 0
	for v := n / u; v >= u; v /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// -------------------------------------------------------------- who is on it

var peerRe = regexp.MustCompile(`([0-9]{1,3}(?:\.[0-9]{1,3}){3}|\[[0-9a-fA-F:]+\]):([0-9]+)`)

// connectedPeers asks the operating system's own tool who is attached to the
// daemon's port. There is no standard-library call for a connection table, and
// a rescue image may not have netstat either -- in which case this says so
// rather than reporting an empty list as if it meant nobody is connected.
func connectedPeers(port int) ([]string, string) {
	var out string
	var err error
	for _, try := range [][]string{
		{"netstat", "-an"},
		{"ss", "-tn"},
	} {
		out, err = runCapture(try[0], try[1:], 5*time.Second)
		if err == nil && strings.TrimSpace(out) != "" {
			break
		}
	}
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, "neither netstat nor ss is present here, so who is connected " +
			"cannot be read. The daemon check above is unaffected."
	}
	want := ":" + strconv.Itoa(port)
	seen := map[string]bool{}
	var peers []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, want) || !strings.Contains(strings.ToUpper(line), "ESTAB") {
			continue
		}
		m := peerRe.FindAllStringSubmatch(line, -1)
		// A row has a local and a remote endpoint; the one that is not our
		// port is the director.
		for _, g := range m {
			if g[2] == strconv.Itoa(port) {
				continue
			}
			p := g[1] + ":" + g[2]
			if !seen[p] {
				seen[p] = true
				peers = append(peers, p)
			}
		}
	}
	return peers, ""
}

// ----------------------------------------------------------------- the state

func buildState(e Env, s *sampler) uiState {
	host, _ := os.Hostname()
	st := uiState{
		Host:    host,
		OS:      e.OS,
		Version: version,
		Now:     time.Now().Format("2006-01-02 15:04:05"),
	}

	st.Env = append(st.Env,
		uiCheck{Name: "file daemon", OK: e.FdBin != "", Detail: orNone(e.FdBin)},
		uiCheck{Name: "configuration", OK: e.ConfigDir != "", Detail: orNone(e.ConfigDir)},
		uiCheck{Name: "bconsole", OK: e.Bconsole != "", Detail: orNone(e.Bconsole),
			Meaning: "Optional. The Bareos client package for Windows ships no " +
				"bconsole, so its absence is normal on a recovery client."},
	)

	st.Port = fdPort(e)
	all := append([]check{listening(st.Port)}, reachable(st.Port)...)
	ok := true
	for _, c := range all {
		if !c.ok {
			ok = false
		}
		st.Checks = append(st.Checks, uiCheck{
			Name: c.name, OK: c.ok, Detail: c.detail, Meaning: meaningFor(c),
		})
	}
	st.VerdictOK = ok
	if ok {
		st.Verdict = "this machine can receive a restore"
	} else {
		st.Verdict = "something above has to be fixed first"
	}

	if text, err := exportConfig(e); err != nil {
		st.ConfigErr = err.Error()
	} else {
		cfg := parseConfig(text)
		for _, r := range cfg.Resources {
			u := uiResource{Type: r.Type, Name: r.get("Name"), Pairs: map[string]string{}}
			for _, k := range r.Order {
				u.Order = append(u.Order, k)
				u.Pairs[k] = redact(k, r.Keys[k])
			}
			st.Resources = append(st.Resources, u)
		}
	}

	st.Peers, st.PeersNote = connectedPeers(st.Port)

	for _, v := range volumes() {
		free, err := freeSpace(v)
		if err != nil {
			continue
		}
		rate := s.observe(v, free)
		st.Volumes = append(st.Volumes, uiVolume{
			Path: v, FreeBytes: free, Free: humanBytes(free),
			RateMBs: rate, Filling: rate > 0.5,
		})
	}
	return st
}

// ------------------------------------------------------------------ editing

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

func directorDir(e Env) string {
	return filepath.Join(e.ConfigDir, "bareos-fd.d", "director")
}

// writeDirector creates or replaces one Director resource and refuses to
// leave a configuration the daemon will not read. The old content is held in
// memory and put back on failure, so a rejected edit changes nothing at all.
func writeDirector(e Env, name, password string, monitor bool) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("the name %q is not usable: letters, digits, dot, dash "+
			"and underscore only, and it must start with a letter or digit", name)
	}
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("an empty password is the one setting guaranteed to " +
			"fail: the director will report an authentication error, which reads " +
			"like a wrong password rather than a missing one")
	}
	if strings.ContainsAny(password, "\"\n\r") {
		return fmt.Errorf("the password cannot contain a quote or a newline")
	}
	dir := directorDir(e)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, name+".conf")

	prev, hadPrev := []byte(nil), false
	if b, err := os.ReadFile(path); err == nil {
		prev, hadPrev = b, true
	}

	var sb strings.Builder
	sb.WriteString("Director {\n")
	sb.WriteString("  Name = " + name + "\n")
	sb.WriteString("  Password = \"" + password + "\"\n")
	if monitor {
		// A monitor director may ask for status and may do nothing else. It is
		// a different kind of director from the one that runs jobs, and a tray
		// monitor will not connect without one.
		sb.WriteString("  Monitor = yes\n")
	}
	sb.WriteString("}\n")

	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		return err
	}
	if out, err := validate(e); err != nil {
		if hadPrev {
			os.WriteFile(path, prev, 0o600)
		} else {
			os.Remove(path)
		}
		return fmt.Errorf("the daemon refused this configuration, so nothing was "+
			"changed:\n%s", out)
	}
	return nil
}

func deleteDirector(e Env, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("bad name")
	}
	path := filepath.Join(directorDir(e), name+".conf")
	prev, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no file for a director called %q", name)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if out, err := validate(e); err != nil {
		os.WriteFile(path, prev, 0o600)
		return fmt.Errorf("removing it left a configuration the daemon will not "+
			"read, so it was put back:\n%s", out)
	}
	return nil
}

// validate asks the daemon to read the configuration and report. -t is the
// only opinion that counts: this program's own parser is for display.
func validate(e Env) (string, error) {
	if e.FdBin == "" {
		return "", fmt.Errorf("no file daemon binary was found, so the " +
			"configuration cannot be checked")
	}
	out, err := runCapture(e.FdBin, []string{"-t", "-c", e.ConfigDir}, 20*time.Second)
	return strings.TrimSpace(out), err
}

// ----------------------------------------------------------------- the pages

type server struct {
	env   Env
	token string
	samp  *sampler
	tmpl  *template.Template
}

func (s *server) authed(r *http.Request) bool {
	return r.FormValue("k") == s.token
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buildState(s.env, s.samp))
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	st := buildState(s.env, s.samp)
	data := struct {
		S     uiState
		Token string
		Dir   string
	}{st, s.token, directorDir(s.env)}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, data); err != nil {
		fmt.Fprintln(os.Stderr, "template:", err)
	}
}

func (s *server) handleDirector(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.authed(r) {
		http.Error(w, "the key printed on the console is required to change "+
			"anything; reading is open", http.StatusForbidden)
		return
	}
	var err error
	switch r.FormValue("action") {
	case "delete":
		err = deleteDirector(s.env, r.FormValue("name"))
	default:
		err = writeDirector(s.env, r.FormValue("name"), r.FormValue("password"),
			r.FormValue("monitor") == "yes")
	}
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, err.Error())
		return
	}
	http.Redirect(w, r, "/?k="+s.token, http.StatusSeeOther)
}

func serveMain(e Env, addr string) error {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	s := &server{env: e, token: hex.EncodeToString(raw), samp: newSampler()}
	var err error
	s.tmpl, err = template.New("page").Parse(pageHTML)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/director", s.handleDirector)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Println(bar("BMR CONSOLE, IN A BROWSER"))
	for _, ip := range serveURLs(ln.Addr().(*net.TCPAddr)) {
		fmt.Printf("  http://%s/?k=%s\n", ip, s.token)
	}
	fmt.Println()
	fmt.Println("  Reading is open to anyone who can reach this address.")
	fmt.Println("  Changing the configuration needs the key in those URLs, which")
	fmt.Println("  is new every time this starts. There is no other authentication:")
	fmt.Println("  serve this on a recovery network, not on one you share.")
	fmt.Println()
	fmt.Println("  Ctrl-C to stop.")
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

// serveURLs lists the addresses the page will actually answer on, because
// "listening on :8080" is not something you can type into a browser on
// another machine.
func serveURLs(a *net.TCPAddr) []string {
	port := strconv.Itoa(a.Port)
	if !a.IP.IsUnspecified() {
		return []string{net.JoinHostPort(a.IP.String(), port)}
	}
	out := []string{net.JoinHostPort("127.0.0.1", port)}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, ad := range addrs {
		n, ok := ad.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.To4() == nil {
			continue
		}
		out = append(out, net.JoinHostPort(n.IP.String(), port))
	}
	return out
}
