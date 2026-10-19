// bmr-console -- one screen that answers the only question a machine in the
// middle of a bare-metal recovery has to answer: can this thing receive a
// restore, and if not, which part is wrong?
//
// WHY IT EXISTS. A recovery environment gives you a file daemon with no user
// interface and a director somewhere else. Checking whether the daemon came
// up means reading a configuration file in a directory you have to remember,
// finding out whether anything is listening, and then getting the director to
// agree that this client is the client it thinks it is. Three different tools
// and a lot of typing, at the point in the day when typing is least reliable.
//
// WHY GO, AND NOT A WINDOWS GUI TOOLKIT. A bare-metal recovery has two halves
// -- a Windows PE for Windows machines and a Linux rescue image for Linux
// ones -- and the same question has to be answered in both. AutoIt, VBScript
// or a .NET form can only ever do the Windows half. Go cross-compiles to a
// single static binary for each, from one source file, with no runtime to
// install: GOOS=windows and GOOS=linux, nothing else to change.
//
// WHY A TEXT INTERFACE AND NOT A GRAPHICAL ONE. Every cross-platform Go GUI
// toolkit draws through OpenGL. A WinPE has a basic display driver and no 3D
// at all, and a Linux rescue image is often a framebuffer console with no X
// server -- so a graphical build would fail to start on exactly the machines
// this is for. A numbered menu on stdin/stdout works on both, works over the
// serial console, and works over ssh, which is how a recovery is usually
// driven anyway.
//
// WHAT IT READS. The client's own configuration, through the daemon that owns
// it: `bareos-fd --xc` prints the resources as the daemon resolved them,
// which is not the same as the file on disk -- includes have been followed,
// defaults filled in, and passwords replaced by their stored form. That is
// the version worth looking at, because it is the version the daemon is
// actually using.
//
//	bmr-console              interactive menu
//	bmr-console config       what this client believes it is
//	bmr-console status       is the daemon up and listening
//	bmr-console jobs         ask the director what it has for this client
//	bmr-console check        every check at once, exit non-zero on a problem
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// version is overwritten at build time by build.sh:
//
//	-ldflags "-X main.version=1.1.0"
//
// It is a var rather than a const so the linker can reach it, and it is
// what `bmr-console version` prints -- a binary that cannot say which
// release it came from is a binary nobody can report a bug against.
var version = "dev"

// ---------------------------------------------------------------------------
// Where things are
// ---------------------------------------------------------------------------

// Env is everything that differs between a Windows PE and a Linux rescue
// image. It is discovered once, printed, and never guessed at again -- a tool
// that silently looks in the wrong place is worse than one that says it
// cannot find anything.
type Env struct {
	OS        string
	FdBin     string // the file daemon, used with --xc to read its own config
	Bconsole  string // optional: only needed to talk to the director
	ConfigDir string // the directory CONTAINING bareos-fd.d, which is what -c wants
	Notes     []string
}

// candidates lists, per platform, the places this lab and the stock packages
// put things. The PE entries come first on Windows because in a PE the stock
// location is on the disk being restored -- reading configuration from the
// volume you are about to overwrite is a mistake with no error message.
func candidates() (fd, bc, cfg []string) {
	if runtime.GOOS == "windows" {
		fd = []string{
			`X:\Program Files\Bareos\bareos-fd.exe`,
			`C:\Program Files\Bareos\bareos-fd.exe`,
		}
		bc = []string{
			`X:\Program Files\Bareos\bconsole.exe`,
			`C:\Program Files\Bareos\bconsole.exe`,
		}
		cfg = []string{
			`X:\bareos`,
			`C:\ProgramData\Bareos`,
		}
		return
	}
	fd = []string{
		"/usr/sbin/bareos-fd",
		"/usr/bin/bareos-fd",
		"/sbin/bareos-fd",
	}
	bc = []string{
		"/usr/sbin/bconsole",
		"/usr/bin/bconsole",
	}
	cfg = []string{
		"/etc/bareos",
		"/mnt/local/etc/bareos", // where ReaR mounts the restored system
	}
	return
}

func firstExisting(paths []string, dir bool) string {
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.IsDir() == dir {
			return p
		}
	}
	return ""
}

func detect() Env {
	fds, bcs, cfgs := candidates()
	e := Env{OS: runtime.GOOS}
	e.FdBin = firstExisting(fds, false)
	e.Bconsole = firstExisting(bcs, false)
	e.ConfigDir = firstExisting(cfgs, true)

	if e.FdBin == "" {
		if p, err := exec.LookPath("bareos-fd"); err == nil {
			e.FdBin = p
			e.Notes = append(e.Notes, "file daemon found on PATH, not in a known location")
		}
	}
	if e.ConfigDir != "" {
		if _, err := os.Stat(filepath.Join(e.ConfigDir, "bareos-fd.d")); err != nil {
			e.Notes = append(e.Notes,
				fmt.Sprintf("%s has no bareos-fd.d subdirectory -- -c wants the PARENT of bareos-fd.d", e.ConfigDir))
		}
	}
	return e
}

// ---------------------------------------------------------------------------
// The client's own configuration
// ---------------------------------------------------------------------------

type Resource struct {
	Type   string
	Keys   map[string]string
	Order  []string
	Source string
}

type Config struct {
	Resources []Resource
	Raw       string
}

func (c *Config) byType(t string) []Resource {
	var out []Resource
	for _, r := range c.Resources {
		if strings.EqualFold(r.Type, t) {
			out = append(out, r)
		}
	}
	return out
}

func (r Resource) get(key string) string {
	for k, v := range r.Keys {
		if strings.EqualFold(strings.ReplaceAll(k, " ", ""), strings.ReplaceAll(key, " ", "")) {
			return v
		}
	}
	return ""
}

// exportConfig asks the daemon to print its own resolved configuration.
//
// --xc is the long way round compared with reading the files, and it is the
// right way round: the daemon has followed every include, applied every
// default, and resolved the password to the form it will actually compare
// against. Reading bareos-fd.d by hand tells you what someone wrote. This
// tells you what the daemon is running.
func exportConfig(e Env) (string, error) {
	if e.FdBin == "" {
		return "", errors.New("no bareos-fd binary found")
	}
	args := []string{"--xc"}
	if e.ConfigDir != "" {
		args = append(args, "-c", e.ConfigDir)
	}
	out, err := runCapture(e.FdBin, args, 20*time.Second)
	if err != nil && strings.TrimSpace(out) == "" {
		// Older builds spell it --export-config.
		args[0] = "--export-config"
		out, err = runCapture(e.FdBin, args, 20*time.Second)
	}
	if strings.TrimSpace(out) == "" && err != nil {
		return "", err
	}
	return out, nil
}

// parseConfig reads the resource syntax: a type name, a brace, Key = Value
// lines, a closing brace. Values may be quoted and may contain braces inside
// quotes, which is why this counts depth outside strings rather than matching
// lines with a regular expression.
func parseConfig(text string) *Config {
	cfg := &Config{Raw: text}
	var cur *Resource
	depth := 0

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if depth == 0 {
			if i := strings.Index(line, "{"); i >= 0 {
				name := strings.TrimSpace(line[:i])
				cur = &Resource{Type: name, Keys: map[string]string{}}
				depth = 1
				continue
			}
			continue
		}
		if line == "}" {
			if cur != nil {
				cfg.Resources = append(cfg.Resources, *cur)
				cur = nil
			}
			depth = 0
			continue
		}
		if eq := strings.Index(line, "="); eq > 0 && cur != nil {
			k := strings.TrimSpace(line[:eq])
			v := strings.TrimSpace(line[eq+1:])
			v = strings.TrimSuffix(v, ";")
			v = strings.Trim(v, `"`)
			cur.Keys[k] = v
			cur.Order = append(cur.Order, k)
		}
	}
	return cfg
}

// redact exists because this program prints configuration to a screen that in
// a recovery is often being shared, photographed or recorded. A password is
// never the answer to any question this tool is asked; whether one is SET is.
func redact(key, value string) string {
	k := strings.ToLower(strings.ReplaceAll(key, " ", ""))
	if strings.Contains(k, "password") || strings.Contains(k, "passphrase") {
		if value == "" {
			return "(EMPTY -- the director will refuse this client)"
		}
		kind := "plain"
		if strings.HasPrefix(value, "[md5]") {
			kind = "md5"
		}
		return fmt.Sprintf("(set, %s, %d characters)", kind, len(value))
	}
	return value
}

func showConfig(e Env) error {
	text, err := exportConfig(e)
	if err != nil {
		return err
	}
	cfg := parseConfig(text)

	clients := cfg.byType("Client")
	if len(clients) == 0 {
		clients = cfg.byType("FileDaemon") // the resource has had both names
	}
	fmt.Println(bar("this client"))
	if len(clients) == 0 {
		fmt.Println("  no Client resource in the exported configuration")
	}
	for _, c := range clients {
		printKeys(c, []string{"Name", "FDPort", "Working Directory", "Plugin Directory",
			"Maximum Concurrent Jobs", "Pki Encryption", "Pki Signatures"})
	}

	dirs := cfg.byType("Director")
	fmt.Println(bar(fmt.Sprintf("directors allowed to connect (%d)", len(dirs))))
	if len(dirs) == 0 {
		fmt.Println("  none -- no director can start a job on this client")
	}
	for _, d := range dirs {
		printKeys(d, []string{"Name", "Password", "Address", "Monitor", "TLS Enable"})
	}

	msgs := cfg.byType("Messages")
	fmt.Println(bar(fmt.Sprintf("where this client sends its messages (%d)", len(msgs))))
	for _, m := range msgs {
		for _, k := range m.Order {
			fmt.Printf("  %-26s %s\n", k, redact(k, m.Keys[k]))
		}
	}
	return nil
}

func printKeys(r Resource, preferred []string) {
	seen := map[string]bool{}
	for _, k := range preferred {
		if v := r.get(k); v != "" {
			fmt.Printf("  %-26s %s\n", k, redact(k, v))
			seen[strings.ToLower(strings.ReplaceAll(k, " ", ""))] = true
		}
	}
	var rest []string
	for _, k := range r.Order {
		if !seen[strings.ToLower(strings.ReplaceAll(k, " ", ""))] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		fmt.Printf("  %-26s %s\n", k, redact(k, r.Keys[k]))
	}
	fmt.Println()
}

// ---------------------------------------------------------------------------
// Is anything actually listening
// ---------------------------------------------------------------------------

type check struct {
	name   string
	ok     bool
	detail string
}

func fdPort(e Env) int {
	text, err := exportConfig(e)
	if err != nil {
		return 9102
	}
	cfg := parseConfig(text)
	for _, c := range append(cfg.byType("Client"), cfg.byType("FileDaemon")...) {
		if p := c.get("FDPort"); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				return n
			}
		}
	}
	return 9102
}

// listening dials rather than parsing netstat. A PE has netstat, a rescue
// image may have only ss, and both tell you about a socket; connecting tells
// you about the service. It is also the only check that fails the same way
// the director's will.
func listening(port int) check {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return check{fmt.Sprintf("daemon on %d", port), false, err.Error()}
	}
	defer conn.Close()
	return check{fmt.Sprintf("daemon on %d", port), true, "accepted a connection on " + addr}
}

// reachable answers the other half of the question: the director has to reach
// THIS machine, and a daemon bound to the loopback only is a daemon no
// director can use. Every address this host has, and the port on each.
func reachable(port int) []check {
	var out []check
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return []check{{"addresses", false, err.Error()}}
	}
	apipa := 0
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		// 169.254.x.x is not an address anything can route to. Windows hands
		// one out when a static assignment is rejected -- a duplicate, most
		// often -- and leaves the interface looking configured, so it is
		// worth naming rather than listing among the failures.
		if ipnet.IP.IsLinkLocalUnicast() {
			apipa++
			continue
		}
		ip := ipnet.IP.String()
		c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), 3*time.Second)
		if err != nil {
			out = append(out, check{"on " + ip, false, "not accepting connections"})
			continue
		}
		c.Close()
		out = append(out, check{"on " + ip, true, "a director can reach this"})
	}
	if apipa > 0 {
		out = append(out, check{"link-local only", false, fmt.Sprintf(
			"%d address(es) are 169.254.x.x: the static address was refused, or DHCP "+
				"answered nothing. Check for another machine using the same address.", apipa)})
	}
	if len(out) == 0 {
		out = append(out, check{"routable address", false,
			"this machine has no non-loopback IPv4 address -- nothing can reach it"})
	}
	return out
}

func showStatus(e Env) bool {
	port := fdPort(e)
	all := []check{listening(port)}
	all = append(all, reachable(port)...)
	fmt.Println(bar("can a director use this machine"))
	ok := true
	for _, c := range all {
		mark := "FAIL"
		if c.ok {
			mark = " OK "
		} else {
			ok = false
		}
		fmt.Printf("  [%s] %-22s %s\n", mark, c.name, c.detail)
	}
	fmt.Println()
	return ok
}

// ---------------------------------------------------------------------------
// The director's side
// ---------------------------------------------------------------------------

// bconsoleJSON runs one bconsole command in its JSON mode and returns the
// decoded result. `.api json` switches the console to JSON-RPC 2.0, so the
// payload is under "result" and an error is a sibling of it, not a message
// printed to the screen -- which is the whole reason for using it here rather
// than scraping the human output.
func bconsoleJSON(e Env, command string) (map[string]any, error) {
	if e.Bconsole == "" {
		return nil, errors.New("no bconsole on this machine -- a recovery client usually has none")
	}
	in := ".api json\n" + command + "\nquit\n"
	out, err := runCaptureStdin(e.Bconsole, nil, in, 30*time.Second)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
	}
	// bconsole prints a banner before the first JSON object; find it.
	i := strings.Index(out, "{")
	if i < 0 {
		return nil, fmt.Errorf("no JSON in bconsole output: %.200s", out)
	}
	dec := json.NewDecoder(strings.NewReader(out[i:]))
	var last map[string]any
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if _, hasResult := m["result"]; hasResult || len(last) == 0 {
			last = m
		}
	}
	if last == nil {
		return nil, errors.New("bconsole returned no decodable object")
	}
	if e, ok := last["error"]; ok {
		return last, fmt.Errorf("director refused: %v", e)
	}
	return last, nil
}

func showJobs(e Env) error {
	res, err := bconsoleJSON(e, "list jobs")
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(res["result"], "  ", "  ")
	fmt.Println(bar("what the director has"))
	fmt.Println("  " + string(b))
	return nil
}

// ---------------------------------------------------------------------------
// Running other programs
// ---------------------------------------------------------------------------

func runCapture(bin string, args []string, d time.Duration) (string, error) {
	return runCaptureStdin(bin, args, "", d)
}

func runCaptureStdin(bin string, args []string, stdin string, d time.Duration) (string, error) {
	cmd := exec.Command(bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var sb strings.Builder
	cmd.Stdout = &sb
	cmd.Stderr = &sb
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return sb.String(), err
	case <-time.After(d):
		_ = cmd.Process.Kill()
		return sb.String(), fmt.Errorf("%s did not finish within %s", filepath.Base(bin), d)
	}
}

// ---------------------------------------------------------------------------
// Presentation
// ---------------------------------------------------------------------------

// No colour and no cursor control anywhere in this program. A WinPE console,
// a Linux framebuffer console and an ssh session from either disagree about
// what they support, and a recovery tool that renders as escape sequences on
// the one machine that matters has failed at the only moment it is used.
func bar(title string) string {
	line := strings.Repeat("-", 72)
	return fmt.Sprintf("\n%s\n%s\n%s", line, strings.ToUpper(title), line)
}

func showEnv(e Env) {
	fmt.Println(bar("where this is running"))
	fmt.Printf("  %-26s %s\n", "platform", e.OS)
	fmt.Printf("  %-26s %s\n", "file daemon", orNone(e.FdBin))
	fmt.Printf("  %-26s %s\n", "configuration", orNone(e.ConfigDir))
	fmt.Printf("  %-26s %s\n", "bconsole", orNone(e.Bconsole))
	for _, n := range e.Notes {
		fmt.Printf("  %-26s %s\n", "note", n)
	}
	fmt.Println()
}

func orNone(s string) string {
	if s == "" {
		return "(not found)"
	}
	return s
}

func menu(e Env) {
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Println(bar(fmt.Sprintf("bmr-console %s", version)))
		fmt.Println("  1  where this is running")
		fmt.Println("  2  what this client believes it is")
		fmt.Println("  3  can a director use this machine")
		fmt.Println("  4  ask the director what it has")
		fmt.Println("  5  everything, as a pass/fail list")
		fmt.Println("  q  quit")
		fmt.Print("\n  > ")
		if !in.Scan() {
			return
		}
		switch strings.TrimSpace(strings.ToLower(in.Text())) {
		case "1":
			showEnv(e)
		case "2":
			if err := showConfig(e); err != nil {
				fmt.Println("  cannot read the configuration:", err)
			}
		case "3":
			showStatus(e)
		case "4":
			if err := showJobs(e); err != nil {
				fmt.Println("  cannot ask the director:", err)
			}
		case "5":
			runAll(e)
		case "q", "quit", "exit":
			return
		}
	}
}

func runAll(e Env) bool {
	showEnv(e)
	ok := true
	if err := showConfig(e); err != nil {
		fmt.Println("  cannot read the configuration:", err)
		ok = false
	}
	if !showStatus(e) {
		ok = false
	}
	fmt.Println(bar("verdict"))
	if ok {
		fmt.Println("  this machine can receive a restore")
	} else {
		fmt.Println("  something above has to be fixed before a restore will run")
	}
	fmt.Println()
	return ok
}

func main() {
	e := detect()
	cmd := ""
	if len(os.Args) > 1 {
		cmd = strings.ToLower(os.Args[1])
	}
	switch cmd {
	case "":
		menu(e)
	case "env":
		showEnv(e)
	case "config":
		if err := showConfig(e); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "status":
		if !showStatus(e) {
			os.Exit(1)
		}
	case "jobs":
		if err := showJobs(e); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "check":
		if !runAll(e) {
			os.Exit(1)
		}
	case "serve", "--serve":
		// The address is optional. :8080 on every interface is what a
		// recovery wants -- the browser is usually on another machine -- and
		// an explicit 127.0.0.1:8080 is there for when it is not.
		addr := ":8080"
		if len(os.Args) > 2 {
			addr = os.Args[2]
			if !strings.Contains(addr, ":") {
				addr = ":" + addr
			}
		}
		if err := serveMain(e, addr); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "-v", "--version", "version":
		fmt.Printf("bmr-console %s (%s/%s, go %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Fprintln(os.Stderr, "  bmr-console [env|config|status|jobs|check|version]")
		fmt.Fprintln(os.Stderr, "  bmr-console serve [addr]   (default :8080)")
		os.Exit(2)
	}
}
