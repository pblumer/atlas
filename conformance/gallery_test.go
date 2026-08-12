package conformance

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The interactive gallery: a single self-contained page served by the Atlas web
// server (api/web/conformance-gallery.html) that lists every scenario with its
// diagram, description, expected outcome, and a button that deploys and runs it
// against the live REST API — same-origin, so no CORS is needed. It is generated
// from the scenario registry so it never drifts from the executable suite.
//
// Regenerate with `go test ./conformance -update`.

var firstProcessRe = regexp.MustCompile(`<process[^>]*\bid="([^"]+)"`)

type galleryScenario struct {
	Name         string            `json:"name"`
	Title        string            `json:"title"`
	Desc         string            `json:"desc"`
	Diagram      string            `json:"diagram"`
	Features     []string          `json:"features"`
	Patterns     []string          `json:"patterns"`
	Verified     []string          `json:"verified"`
	Autorun      bool              `json:"autorun"`
	Proc         string            `json:"proc"`
	Hint         string            `json:"hint"`
	ExpCompleted bool              `json:"expCompleted"`
	ExpPath      []string          `json:"expPath"`
	ExpVars      map[string]string `json:"expVars"`
	XML          string            `json:"xml"`
}

type galleryModel struct {
	Title string `json:"title"`
	XML   string `json:"xml"`
}

func TestGalleryUpToDate(t *testing.T) {
	var scns []galleryScenario
	var models []galleryModel
	seenModel := map[string]bool{}

	for _, sc := range Scenarios {
		model, err := sc.LoadModel()
		if err != nil {
			t.Fatalf("load %s: %v", sc.Name, err)
		}
		res, err := Run(t.TempDir(), model, sc.Root, sc.Start, sc.Driver)
		if err != nil {
			t.Fatalf("run %s: %v", sc.Name, err)
		}
		vars := res.Variables
		if vars == nil {
			vars = map[string]string{}
		}
		path := res.Path
		if path == nil {
			path = []string{}
		}
		feats := sc.Features
		if feats == nil {
			feats = []string{}
		}
		pats := sc.PatternsOf()
		if pats == nil {
			pats = []string{}
		}
		scns = append(scns, galleryScenario{
			Name:         sc.Name,
			Title:        titleOf(sc, model),
			Desc:         galleryDesc(model),
			Diagram:      strings.TrimSuffix(sc.Model, ".bpmn"),
			Features:     feats,
			Patterns:     pats,
			Verified:     verifiedLabels(sc),
			Autorun:      sc.Start.kind == startExplicit && len(sc.Driver) == 0,
			Proc:         rootProcessID(sc, model),
			Hint:         driveHint(sc),
			ExpCompleted: res.State.String() == "completed",
			ExpPath:      path,
			ExpVars:      vars,
			XML:          string(model),
		})
		if !seenModel[sc.Model] {
			seenModel[sc.Model] = true
			models = append(models, galleryModel{Title: modelTitle(model), XML: string(model)})
		}
	}

	scnJSON, err := json.Marshal(scns)
	if err != nil {
		t.Fatalf("marshal scenarios: %v", err)
	}
	modelJSON, err := json.Marshal(models)
	if err != nil {
		t.Fatalf("marshal models: %v", err)
	}
	html := galleryTemplate
	html = strings.Replace(html, "__SCENARIOS__", string(scnJSON), 1)
	html = strings.Replace(html, "__MODELS__", string(modelJSON), 1)

	checkTCKFile(t, filepath.Join("..", "api", "web", "conformance-gallery.html"), []byte(html))
}

// titleOf prefers the process name over the scenario key for display.
func titleOf(sc Scenario, model []byte) string {
	if n := modelTitle(model); n != "" {
		return n
	}
	return sc.Name
}

var processNameRe = regexp.MustCompile(`<process[^>]*\bname="([^"]+)"`)

func modelTitle(model []byte) string {
	if m := processNameRe.FindSubmatch(model); m != nil {
		return string(m[1])
	}
	return ""
}

func rootProcessID(sc Scenario, model []byte) string {
	if sc.Root != "" {
		return sc.Root
	}
	if m := firstProcessRe.FindSubmatch(model); m != nil {
		return string(m[1])
	}
	return ""
}

// galleryDesc lifts the first paragraph of the model's leading comment, unescaped
// (the page sets it via textContent, which handles escaping).
func galleryDesc(model []byte) string {
	d := modelDescription(model)
	if i := strings.Index(d, "\n\n"); i >= 0 {
		d = d[:i]
	}
	d = strings.ReplaceAll(d, "&lt;", "<")
	d = strings.ReplaceAll(d, "&gt;", ">")
	d = strings.ReplaceAll(d, "&amp;", "&")
	return d
}

func verifiedLabels(sc Scenario) []string {
	v := []string{"Golden trace", "Replay (I4)", "Invarianten"}
	if len(equivPeers(sc)) > 0 {
		v = append(v, "Metamorphic")
	}
	if differentialSubset[sc.Name] {
		v = append(v, "Differential")
	}
	return v
}

func driveHint(sc Scenario) string {
	switch sc.Start.kind {
	case startMessage:
		return "Message-Start — Nachricht \"" + sc.Start.message + "\" publizieren (Key " + sc.Start.correlation + ")"
	case startTimer:
		return "Timer-Start — feuert nach " + sc.Start.after.String()
	case startSignal:
		return "Signal-Start — Pool \"" + sc.Start.trigger + "\" starten; sein Broadcast gebiert die Instanz"
	}
	if len(sc.Driver) == 0 {
		return ""
	}
	s0 := sc.Driver[0]
	switch s0.kind {
	case stepComplete:
		return "Parkt auf Job \"" + s0.element + "\" — in Operations/Inbox abschließen"
	case stepPublish:
		return "Wartet auf Nachricht \"" + s0.message + "\" (Key " + s0.correlation + ")"
	case stepWait:
		return "Feuert nach " + s0.after.String()
	case stepThrowError:
		return "Job \"" + s0.element + "\" wirft Business-Error \"" + s0.message + "\""
	case stepFail:
		return "Job \"" + s0.element + "\" schlägt fehl → Incident"
	}
	return "Interaktiv steuerbar"
}

const galleryTemplate = `<!doctype html>
<html lang="de">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>BPMN Conformance — Interaktive Galerie</title>
<style>
:root{
  --bg:#f6f7f9; --fg:#1c2024; --muted:#5b636c; --card:#ffffff; --border:#e3e6ea;
  --accent:#2d6cdf; --accent-fg:#ffffff; --chip:#eef1f5; --ok:#1a7f4b; --warn:#b26a00; --err:#c0392b;
}
@media (prefers-color-scheme: dark){:root{
  --bg:#15181c; --fg:#e6e9ec; --muted:#9aa3ad; --card:#1e2228; --border:#2c323a;
  --accent:#5b8def; --accent-fg:#0d1117; --chip:#262c34; --ok:#4cc38a; --warn:#e0a44a; --err:#e5707a;
}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
header{padding:24px clamp(16px,4vw,48px);border-bottom:1px solid var(--border);background:var(--card)}
header h1{margin:0 0 6px;font-size:22px}
header p{margin:0;color:var(--muted);max-width:70ch}
.bar{display:flex;flex-wrap:wrap;gap:10px;align-items:center;margin-top:16px}
.bar input{flex:1 1 260px;min-width:200px;padding:8px 10px;border:1px solid var(--border);border-radius:8px;background:var(--bg);color:var(--fg)}
button{cursor:pointer;border:1px solid var(--border);border-radius:8px;padding:8px 14px;font:inherit;background:var(--card);color:var(--fg)}
button.primary,button.run{background:var(--accent);color:var(--accent-fg);border-color:transparent;font-weight:600}
button:disabled{opacity:.55;cursor:default}
.status{margin-top:12px;font-size:14px;color:var(--muted);min-height:20px}
.status.ok{color:var(--ok)} .status.err{color:var(--err)}
main{padding:clamp(16px,4vw,48px);display:grid;grid-template-columns:repeat(auto-fill,minmax(340px,1fr));gap:20px}
.card{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:16px;display:flex;flex-direction:column;gap:10px}
.card h3{margin:0;font-size:16px;display:flex;flex-wrap:wrap;align-items:baseline;gap:8px}
.card h3 .pid{font-size:12px;font-weight:500;color:var(--muted);font-family:ui-monospace,monospace}
.diagram{width:100%;background:#fff;border:1px solid var(--border);border-radius:8px;max-height:210px;object-fit:contain}
.desc{margin:0;color:var(--muted);font-size:13.5px}
.tags,.verified{display:flex;flex-wrap:wrap;gap:6px}
.tag{font-size:11px;font-family:ui-monospace,monospace;background:var(--chip);color:var(--muted);padding:2px 7px;border-radius:999px}
.tag.pat{color:var(--accent)}
.verified .v{font-size:11px;color:var(--muted)}
.verified .v::before{content:"✓ ";color:var(--ok)}
.hint{font-size:12.5px;color:var(--muted)}
.out{font-size:13px;margin-top:2px}
.out.err{color:var(--err)} .out.ok{color:var(--ok)}
.verdict{font-weight:600;margin-bottom:6px}
.verdict.ok{color:var(--ok)} .verdict.warn{color:var(--warn)}
.pathrow{display:flex;flex-wrap:wrap;align-items:center;gap:4px;margin-top:4px}
.pathrow .lbl{font-size:11px;color:var(--muted);text-transform:uppercase;letter-spacing:.04em;margin-right:4px}
.chip{font-family:ui-monospace,monospace;font-size:12px;background:var(--chip);padding:2px 7px;border-radius:6px}
.chip.v{background:transparent;border:1px solid var(--border)}
.arr{color:var(--muted)}
footer{padding:24px clamp(16px,4vw,48px);color:var(--muted);font-size:13px;border-top:1px solid var(--border)}
a{color:var(--accent)}
</style>
</head>
<body>
<header>
  <h1>BPMN Conformance — Interaktive Galerie</h1>
  <p>Jedes Szenario der Conformance-Suite: Diagramm, Beschreibung, erwartetes Ergebnis und ein Knopf, der es
     gegen diese Atlas-Instanz deployt und ausführt. Die Aufrufe gehen an die REST-API derselben Origin —
     öffne diese Seite von deiner Instanz aus (<code>/conformance-gallery.html</code>), dann ist kein CORS nötig.</p>
  <div class="bar">
    <input id="base" placeholder="API-Basis-URL (leer = gleiche Origin, z. B. https://deine-instanz)">
    <button id="deployAllBtn" class="primary">⤓ Alle in ein neues Projekt deployen</button>
  </div>
  <div id="status" class="status"></div>
</header>
<main id="grid"></main>
<footer>
  Generiert aus <code>conformance/scenario.go</code>. „Deploy &amp; Run" deployt das Modell, startet eine Instanz
  und vergleicht den Live-Pfad mit dem erwarteten Golden-Ergebnis. Geparkte/getriebene Szenarien werden nur
  deployt — starte und treibe sie in der Operations-/Inbox-Ansicht (Hinweis je Karte).
</footer>
<script>
const SCENARIOS = __SCENARIOS__;
const MODELS = __MODELS__;

function baseUrl(){ return document.getElementById('base').value.trim().replace(/\/+$/,''); }
function setStatus(msg, kind){ const s=document.getElementById('status'); s.textContent=msg; s.className='status '+(kind||''); }
function esc(s){ const d=document.createElement('div'); d.textContent=String(s); return d.innerHTML; }

async function apiCall(method, path, body, isXml){
  const opts={method, headers:{}};
  if(body!==undefined && body!==null){
    if(isXml){ opts.body=body; opts.headers['Content-Type']='application/xml'; }
    else { opts.body=JSON.stringify(body); opts.headers['Content-Type']='application/json'; }
  }
  const res=await fetch(baseUrl()+path, opts);
  const text=await res.text();
  let data=text; try{ data=JSON.parse(text); }catch(e){}
  if(!res.ok){ throw new Error((data&&data.error)?data.error:('HTTP '+res.status+' '+text.slice(0,120))); }
  return data;
}

async function deployAll(){
  const btn=document.getElementById('deployAllBtn'); btn.disabled=true;
  try{
    setStatus('Lege Projekt an …');
    const proj=await apiCall('POST','/api/v1/projects',{name:'BPMN Conformance (gallery)'});
    for(let i=0;i<MODELS.length;i++){
      setStatus('Speichere Draft '+(i+1)+'/'+MODELS.length+': '+MODELS[i].title+' …');
      await apiCall('POST','/api/v1/drafts?projectId='+encodeURIComponent(proj.id), MODELS[i].xml, true);
    }
    setStatus('Deploye Projekt …');
    const dep=await apiCall('POST','/api/v1/projects/'+encodeURIComponent(proj.id)+'/deploy', {});
    const n=(dep&&dep.definitions)?dep.definitions.length:0;
    setStatus('✓ Projekt "'+proj.name+'" deployt — '+n+' Prozessdefinitionen (Projekt-ID '+proj.id+'). Öffne es im Modeler.','ok');
  }catch(e){ setStatus('Fehler: '+e.message,'err'); }
  btn.disabled=false;
}

function renderResult(out, s, state, path, vars){
  const completed = state==='completed';
  const pathOk = JSON.stringify(path)===JSON.stringify(s.expPath);
  let head, cls;
  if(completed && pathOk){ head='✓ Ergebnis wie erwartet'; cls='ok'; }
  else if(completed){ head='⚠ abgeschlossen, abweichender Pfad'; cls='warn'; }
  else { head='⏸ geparkt ('+esc(state)+')'; cls='warn'; }
  let html='<div class="verdict '+cls+'">'+head+'</div>';
  html+='<div class="pathrow"><span class="lbl">Pfad</span>'+path.map(p=>'<span class="chip">'+esc(p)+'</span>').join('<span class="arr">→</span>')+'</div>';
  const vk=Object.keys(vars).sort();
  if(vk.length){ html+='<div class="pathrow"><span class="lbl">Variablen</span>'+vk.map(k=>'<span class="chip v">'+esc(k)+' = '+esc(vars[k])+'</span>').join(' ')+'</div>'; }
  out.innerHTML=html; out.className='out';
}

async function runCard(idx, btn){
  const s=SCENARIOS[idx]; const out=document.getElementById('out-'+idx);
  btn.disabled=true; out.className='out'; out.textContent='Läuft …';
  try{
    const dep=await apiCall('POST','/api/v1/deployments', s.xml, true);
    let key=dep.key;
    if(dep.deployments){ const m=dep.deployments.find(d=>d.processId===s.proc); if(m) key=m.key; }
    await apiCall('POST','/api/v1/processes/'+key+'/instances', {});
    const list=await apiCall('GET','/api/v1/instances?process='+key);
    if(!Array.isArray(list)||!list.length) throw new Error('keine Instanz gefunden');
    const inst=list.reduce((a,b)=> (b.key>a.key?b:a));
    const tl=await apiCall('GET','/api/v1/instances/'+inst.key+'/timeline');
    const steps=(tl&&tl.steps)||[];
    const path=steps.map(x=>x.elementId);
    const lastVars=steps.length?(steps[steps.length-1].variables||[]):[];
    const vars={}; lastVars.forEach(v=>{ vars[v.name]=String(v.value); });
    renderResult(out, s, inst.state, path, vars);
  }catch(e){ out.className='out err'; out.textContent='Fehler: '+e.message; }
  btn.disabled=false;
}

async function deployCard(idx, btn){
  const s=SCENARIOS[idx]; const out=document.getElementById('out-'+idx);
  btn.disabled=true; out.className='out'; out.textContent='Deploye …';
  try{
    const dep=await apiCall('POST','/api/v1/deployments', s.xml, true);
    out.className='out ok'; out.textContent='✓ Deployt (Key '+dep.key+'). '+(s.hint||'In Operations starten und treiben.');
  }catch(e){ out.className='out err'; out.textContent='Fehler: '+e.message; }
  btn.disabled=false;
}

function renderCards(){
  const grid=document.getElementById('grid');
  SCENARIOS.forEach((s,idx)=>{
    const c=document.createElement('div'); c.className='card';
    let h='';
    h+='<h3>'+esc(s.title)+' <span class="pid">'+esc(s.name)+'</span></h3>';
    h+='<img class="diagram" loading="lazy" src="conformance-diagrams/'+esc(s.diagram)+'.png" alt="Diagramm: '+esc(s.title)+'">';
    h+='<p class="desc">'+esc(s.desc)+'</p>';
    h+='<div class="tags">'+(s.features||[]).map(f=>'<span class="tag">'+esc(f)+'</span>').join('')+(s.patterns||[]).map(p=>'<span class="tag pat">'+esc(p)+'</span>').join('')+'</div>';
    h+='<div class="verified">'+(s.verified||[]).map(v=>'<span class="v">'+esc(v)+'</span>').join('')+'</div>';
    if(s.autorun){ h+='<button class="run" onclick="runCard('+idx+',this)">▶ Deploy &amp; Run</button>'; }
    else { h+='<button class="run" onclick="deployCard('+idx+',this)">⤓ Deploy</button>'; if(s.hint){ h+='<span class="hint">'+esc(s.hint)+'</span>'; } }
    h+='<div class="out" id="out-'+idx+'"></div>';
    c.innerHTML=h; grid.appendChild(c);
  });
}

window.addEventListener('DOMContentLoaded', function(){
  document.getElementById('deployAllBtn').addEventListener('click', deployAll);
  renderCards();
});
</script>
</body>
</html>
`
