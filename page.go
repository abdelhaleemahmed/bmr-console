// page.go -- the one page serve mode draws.
//
// Everything is inline: no stylesheet, no script, no font and no image comes
// from anywhere else. A recovery network has no internet, and a page that
// renders as unstyled text because a CDN did not answer is a page that failed
// at the only moment it was needed. The same reasoning as the rest of this
// program: carry it, do not fetch it.
//
// The HTML below is a Go raw string, so it must contain no backquote.
package main

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.S.Host}} -- can this machine receive a restore?</title>
<style>
  :root {
    --bg:#11151a; --panel:#1a2029; --line:#2b3542; --fg:#e7edf3;
    --dim:#9fb0c0; --ok:#4ec07a; --bad:#e2605a; --warn:#e0a84e; --accent:#64b5f6;
  }
  @media (prefers-color-scheme: light) {
    :root:not([data-theme="dark"]) {
      --bg:#f4f6f8; --panel:#ffffff; --line:#d7dee6; --fg:#16202b;
      --dim:#5a6b7b; --ok:#1e7a46; --bad:#b3302a; --warn:#8a6414; --accent:#1667b1;
    }
  }
  * { box-sizing:border-box; }
  body { margin:0; background:var(--bg); color:var(--fg);
         font:15px/1.55 ui-sans-serif,system-ui,"Segoe UI",Roboto,Helvetica,Arial,sans-serif; }
  .wrap { max-width:960px; margin:0 auto; padding:16px; }
  h1 { font-size:20px; margin:18px 0 4px; }
  h2 { font-size:15px; letter-spacing:.06em; text-transform:uppercase;
       color:var(--dim); margin:26px 0 10px; font-weight:600; }
  .sub { color:var(--dim); margin:0 0 6px; }
  .panel { background:var(--panel); border:1px solid var(--line);
           border-radius:10px; padding:14px 16px; margin:10px 0; }
  .verdict { font-size:18px; font-weight:600; padding:14px 16px; border-radius:10px;
             border:1px solid var(--line); margin:12px 0 4px; }
  .verdict.ok  { color:var(--ok);  border-color:var(--ok); }
  .verdict.bad { color:var(--bad); border-color:var(--bad); }
  .row { display:flex; gap:12px; padding:7px 0; border-bottom:1px solid var(--line); }
  .row:last-child { border-bottom:0; }
  .tag { flex:0 0 150px; font-weight:600; }
  .val { flex:1 1 auto; min-width:0; word-break:break-word; }
  .mark { flex:0 0 2.2em; font-weight:700; }
  .mark.ok  { color:var(--ok); }
  .mark.bad { color:var(--bad); }
  .why { color:var(--warn); font-size:13.5px; margin-top:4px; }
  code, .mono { font-family:ui-monospace,SFMono-Regular,Consolas,"Liberation Mono",monospace;
                font-size:13.5px; }
  table { width:100%; border-collapse:collapse; }
  td { padding:5px 8px 5px 0; vertical-align:top; border-bottom:1px solid var(--line); }
  td.k { color:var(--dim); white-space:nowrap; width:34%; }
  form { display:flex; flex-wrap:wrap; gap:10px; align-items:flex-end; }
  label { display:block; font-size:13px; color:var(--dim); margin-bottom:3px; }
  input[type=text], input[type=password] {
    background:var(--bg); color:var(--fg); border:1px solid var(--line);
    border-radius:6px; padding:7px 9px; font:inherit; min-width:180px; }
  button { background:var(--accent); color:#0b1218; border:0; border-radius:6px;
           padding:8px 14px; font:inherit; font-weight:600; cursor:pointer; }
  button.ghost { background:transparent; color:var(--bad);
                 border:1px solid var(--bad); font-weight:500; }
  .note { color:var(--dim); font-size:13.5px; }
  .bar { height:6px; border-radius:3px; background:var(--line); overflow:hidden; margin-top:5px; }
  .bar > i { display:block; height:100%; background:var(--accent); width:0; }
  .foot { color:var(--dim); font-size:12.5px; margin:28px 0 10px; }
  a.edit { float:right; color:var(--accent); font-size:13.5px; text-decoration:none; }
  a.edit:hover { text-decoration:underline; }
  @media (max-width:560px) {
    .wrap { padding:12px 16px; }
    .tag { flex-basis:100%; }
    .row { flex-wrap:wrap; }
  }
</style>
</head>
<body>
<div class="wrap">

  <h1>{{.S.Host}}</h1>
  <p class="sub mono">{{.S.OS}} &middot; bmr-console {{.S.Version}} &middot; daemon port {{.S.Port}}
     &middot; <span id="now">{{.S.Now}}</span></p>

  <div id="verdict" class="verdict {{if .S.VerdictOK}}ok{{else}}bad{{end}}">{{.S.Verdict}}</div>

  <h2>Can a director use this machine</h2>
  <div class="panel" id="checks">
    {{range .S.Checks}}
    <div class="row">
      <span class="mark {{if .OK}}ok{{else}}bad{{end}}">{{if .OK}}OK{{else}}!!{{end}}</span>
      <span class="tag">{{.Name}}</span>
      <span class="val">{{.Detail}}{{if .Meaning}}<div class="why">{{.Meaning}}</div>{{end}}</span>
    </div>
    {{end}}
  </div>

  <h2>Is a restore happening</h2>
  <div class="panel">
    <p class="note">A file daemon reports progress to its director, not to the
      machine it runs on, so there is no counter here to read. These three
      things are locally true, and they are indirect.</p>
    <div class="row">
      <span class="tag">connected now</span>
      <span class="val" id="peers">
        {{if .S.Peers}}{{range .S.Peers}}<span class="mono">{{.}}</span> {{end}}
        {{else}}<span class="note">nobody is connected to port {{.S.Port}}</span>{{end}}
        {{if .S.PeersNote}}<div class="why">{{.S.PeersNote}}</div>{{end}}
      </span>
    </div>
    {{range .S.Volumes}}
    <div class="row">
      <span class="tag mono">{{.Path}}</span>
      <span class="val">{{.Free}} free
        {{if .Filling}}&mdash; filling at {{printf "%.1f" .RateMBs}} MB/s{{end}}
        <div class="bar"><i style="width:{{if .Filling}}100%{{else}}0{{end}}"></i></div>
      </span>
    </div>
    {{end}}
  </div>

  <h2>What this client believes it is</h2>
  {{if .S.ConfigErr}}
  <div class="panel"><span class="mark bad">!!</span> {{.S.ConfigErr}}</div>
  {{else}}
  {{range .S.Resources}}
  <div class="panel">
    <strong>{{.Type}}</strong> <span class="mono">{{.Name}}</span>
    <a class="edit" href="/edit?k={{$.Token}}&amp;type={{.Type}}&amp;name={{.Name}}">edit</a>
    <table>
      {{$p := .Pairs}}
      {{range .Order}}<tr><td class="k">{{.}}</td><td class="mono">{{index $p .}}</td></tr>{{end}}
    </table>
  </div>
  {{end}}
  {{end}}

  <h2>Add or replace a director</h2>
  <div class="panel">
    <p class="note">Writes one resource into <span class="mono">{{.Dir}}</span>,
      then asks the daemon to read it back. If the daemon refuses, the file is
      put back exactly as it was and nothing changes.</p>
    <form method="post" action="/director">
      <input type="hidden" name="k" value="{{.Token}}">
      <div><label for="n">Name</label>
           <input id="n" type="text" name="name" required
                  placeholder="bareos-dir" autocomplete="off"></div>
      <div><label for="p">Password</label>
           <input id="p" type="password" name="password" required
                  autocomplete="new-password"></div>
      <div><label for="m">Monitor only</label>
           <select id="m" name="monitor" style="padding:7px 9px;border-radius:6px;
                   background:var(--bg);color:var(--fg);border:1px solid var(--line);font:inherit">
             <option value="no">no &mdash; may run jobs</option>
             <option value="yes">yes &mdash; status only</option>
           </select></div>
      <button type="submit">Write it</button>
    </form>
    <p class="note" style="margin-top:12px">A <em>monitor</em> director may ask
      for status and may do nothing else. A tray monitor will not connect
      without one, and it is not the same resource as the director that runs
      jobs.</p>
  </div>

  <h2>Remove a director</h2>
  <div class="panel">
    <form method="post" action="/director">
      <input type="hidden" name="k" value="{{.Token}}">
      <input type="hidden" name="action" value="delete">
      <div><label for="dn">Name</label>
           <input id="dn" type="text" name="name" required autocomplete="off"></div>
      <button class="ghost" type="submit">Remove it</button>
    </form>
  </div>

  <p class="foot">Reading this page needs nothing. Changing anything needs the
    key from the console, which is new every time the server starts.</p>
</div>

<script>
// Poll the same state the page was rendered from. Only the parts that move
// are replaced, so a half-typed password is never wiped out by a refresh.
async function tick() {
  try {
    const r = await fetch("/api/state", {cache: "no-store"});
    if (!r.ok) return;
    const s = await r.json();
    document.getElementById("now").textContent = s.now;
    const v = document.getElementById("verdict");
    v.textContent = s.verdict;
    v.className = "verdict " + (s.verdict_ok ? "ok" : "bad");
    const c = document.getElementById("checks");
    c.innerHTML = s.checks.map(function (x) {
      return '<div class="row"><span class="mark ' + (x.ok ? "ok" : "bad") + '">' +
esc(x.ok ? "OK" : "!!") + '</span><span class="tag">' + esc(x.name) +
             '</span><span class="val">' + esc(x.detail) +
             (x.meaning ? '<div class="why">' + esc(x.meaning) + "</div>" : "") +
             "</span></div>";
    }).join("");
    const p = document.getElementById("peers");
    if (s.peers && s.peers.length) {
      p.innerHTML = s.peers.map(function (x) {
        return '<span class="mono">' + esc(x) + "</span>";
      }).join(" ");
    } else {
      p.innerHTML = '<span class="note">nobody is connected to port ' + s.port + "</span>";
    }
  } catch (e) { /* a poll that fails is not worth a dialog */ }
}
function esc(s) {
  return String(s).replace(/[&<>"']/g, function (ch) {
    return {"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[ch];
  });
}
setInterval(tick, 3000);
</script>
</body>
</html>
`
