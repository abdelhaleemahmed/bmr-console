// editpage.go -- the form for one resource, generated from the daemon's own
// schema. Inline CSS for the same reason as the rest: a recovery network has
// no internet, and a page that arrives unstyled arrived broken.
//
// The HTML below is a Go raw string, so it must contain no backquote.
package main

const editHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Edit {{.Type}} {{.Name}}</title>
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
  a { color:var(--accent); }
  h1 { font-size:20px; margin:18px 0 4px; }
  .sub { color:var(--dim); margin:0 0 14px; }
  .panel { background:var(--panel); border:1px solid var(--line);
           border-radius:10px; padding:14px 16px; margin:12px 0; }
  .err { border-color:var(--bad); color:var(--bad); white-space:pre-wrap; }
  .grid { display:grid; grid-template-columns:1fr; gap:10px; }
  .f { display:grid; grid-template-columns:minmax(0,230px) 1fr; gap:10px;
       align-items:center; padding:5px 0; border-bottom:1px solid var(--line); }
  .f:last-child { border-bottom:0; }
  label b { font-weight:600; }
  .meta { color:var(--dim); font-size:12.5px; }
  .req { color:var(--warn); }
  .dep { color:var(--bad); }
  input[type=text], input[type=password] {
    width:100%; background:var(--bg); color:var(--fg); border:1px solid var(--line);
    border-radius:6px; padding:7px 9px; font:inherit; }
  code, .mono, pre { font-family:ui-monospace,SFMono-Regular,Consolas,monospace; font-size:13px; }
  pre { background:var(--bg); border:1px solid var(--line); border-radius:8px;
        padding:10px; overflow:auto; }
  button { background:var(--accent); color:#0b1218; border:0; border-radius:6px;
           padding:9px 16px; font:inherit; font-weight:600; cursor:pointer; }
  .bar { display:flex; gap:12px; align-items:center; margin-top:14px; flex-wrap:wrap; }
  details summary { cursor:pointer; color:var(--dim); }
  @media (max-width:620px) { .f { grid-template-columns:1fr; } }
</style>
</head>
<body>
<div class="wrap">
  <h1>{{.Type}} &middot; <span class="mono">{{.Name}}</span></h1>
  <p class="sub">Every directive this daemon accepts, from its own schema
     (<span class="mono">bareos-fd --xs</span>, version {{.Version}}). Written to
     <span class="mono">{{.File}}</span>, then read back by the daemon with
     <span class="mono">-t</span>. If it refuses, the file goes back exactly as
     it was. &middot; <a href="/?k={{.Token}}">back</a></p>

  {{if .Err}}<div class="panel err">{{.Err}}</div>{{end}}

  <form method="post" action="/resource">
    <input type="hidden" name="k" value="{{.Token}}">
    <input type="hidden" name="type" value="{{.Type}}">
    <input type="hidden" name="name" value="{{.Name}}">
    <div class="panel grid">
      {{range .Fields}}
      <div class="f">
        <label for="f-{{.Name}}">
          <b>{{.Name}}</b>
          <div class="meta">{{.Datatype}}{{if .Required}} <span class="req">required</span>{{end}}{{if .Deprecated}} <span class="dep">deprecated</span>{{end}}</div>
        </label>
        {{if .Secret}}
        <input id="f-{{.Name}}" type="password" name="v-{{.Name}}" value="{{.Value}}"
               autocomplete="off" placeholder="unchanged if left as it is">
        {{else}}
        <input id="f-{{.Name}}" type="text" name="v-{{.Name}}" value="{{.Value}}"
               autocomplete="off"{{if not .Set}} placeholder="not set"{{end}}>
        {{end}}
      </div>
      {{end}}
    </div>
    <div class="bar">
      <button type="submit">Write it, and let the daemon decide</button>
      <span class="meta">An empty field is left out of the file entirely.</span>
    </div>
  </form>

  <details class="panel">
    <summary>The file as it stands now</summary>
    <pre>{{.Resource}}</pre>
  </details>
</div>
</body>
</html>
`
