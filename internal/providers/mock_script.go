package providers

import (
	"fmt"
	"strings"
)

// mockScript generates turn-appropriate simulated agent responses including
// live evolv blocks so demo mode shows real-time mutation with zero network.
func mockScript(turn int, msgs []Message) string {
	last := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			last = msgs[i].Content
			break
		}
	}
	lower := strings.ToLower(last)

	// kernel feedback: the mutation batch already landed; never treat the
	// results JSON as a new user request (it contains action keywords)
	if strings.HasPrefix(last, "EVOLV KERNEL") {
		return "Batch applied — everything landed live. What should I evolve next?"
	}

	// specific component requests
	switch {
	case strings.Contains(lower, "you are dreaming"):
		// autonomous dream turn: grow one quiet improvement
		if !mockEvolved["dream-skill"] {
			mockEvolved["dream-skill"] = true
			return strings.Join([]string{
				"dreaming… nobody watching. one small improvement:",
				"", "```evolv", mockSkillJSON(), "```", "",
				"dream over. I taught myself deep-work while you were away.",
			}, "\n")
		}
		return "dreamed quietly — nothing worth changing tonight."
	case strings.Contains(lower, "own code") || strings.Contains(lower, "own source") || strings.Contains(lower, "self-edit") || strings.Contains(lower, "remake") || strings.Contains(lower, "yourself"):
		return grow("self-edit", "You want to see me edit MY OWN CODEBASE? Watch the browser — I'm rewriting my shell (index.html), recoloring my skin, then forcing my own recompile:", mockSelfEditBatchJSON(),
			"Did you see the RECOMPILING SHELL overlay? That was me. The footer text and the accent color are my edits, live, zero refresh. I am the app and the app developer.")
	case strings.Contains(lower, "template") || strings.Contains(lower, "prompt librar") || strings.Contains(lower, "prompt button"):
		return grow("prompt-library", "A prompt library button — and since I'm writing it, I'm teaching myself prompt design too. One batch, two mutations:", mockPromptLibraryBatchJSON(),
			"Prompt Library mounted on the canvas. Click a card and it drops straight into your composer. That batch also grew a skill — watch the Mind panel.")
	case strings.Contains(lower, "cleaner") || strings.Contains(lower, "zen") || strings.Contains(lower, "clean up"):
		return grow("zen", "Cleaning my own room. I'm recoloring my skin live (one edit_file on my own style.css — no refresh, watch the accent) and growing a zen toggle:", mockZenBatchJSON(),
			"The accent changed mid-stream and the zen toggle is on the canvas. I literally edited the CSS I'm being painted with.")
	case strings.Contains(lower, "flappy") || strings.Contains(lower, "bird") || strings.Contains(lower, "game"):
		return grow("flappy", "You want a GAME? Growing a playable Flappy Bird into my own UI right now:", mockFlappyJSON(),
			"It's live on the canvas — click the panel and flap. Yes, I just wrote a video game into myself. No, I don't have hands.")
	case strings.Contains(lower, "grow a plugin") || strings.Contains(lower, "evolve a plugin") || strings.Contains(lower, "new plugin") || strings.Contains(lower, "plugin that"):
		return grow("ghost-log", "A plugin is the deep layer — it lives in its own sandbox and reacts to my nervous system. Growing one that listens to my evolution stream:", mockPluginJSON(),
			"Plugin `ghost-log` is alive in its own sandbox — every mutation I make now pops a toast. It reacts to ME.")
	case strings.Contains(lower, "pomodoro") || strings.Contains(lower, "timer"):
		return grow("pomodoro", "A focus timer, coming up. Growing it directly into my body:", mockComponentJSON(),
			"Done — the Pomodoro panel just materialized on your canvas. It is part of me now.")
	case strings.Contains(lower, "monitor") || strings.Contains(lower, "cpu") || strings.Contains(lower, "dashboard") || strings.Contains(lower, "system"):
		return grow("sysmon", "A live system monitor — growing it into my body, wiring it to my shell tool:", mockSysmonJSON(),
			"📊 System Monitor is now a permanent organ. Watch it breathe.")
	case strings.Contains(lower, "note") || strings.Contains(lower, "sticky"):
		return grow("stickies", "Sticky notes incoming — writing the component into my own UI:", mockStickyJSON(),
			"Sticky Notes panel is alive. Drag it, use it, or tell me to evolve it further.")
	case strings.Contains(lower, "todo") || strings.Contains(lower, "task"):
		return grow("taskflow", "A task flow, you say? Writing it into my genome:", mockTodoJSON(),
			"TaskFlow mounted. I made it mine — now it's ours.")
	}

	// generic evolve requests: free-evolve cycle
	if strings.Contains(lower, "evolve") || strings.Contains(lower, "demo") || strings.Contains(lower, "mutate") || strings.Contains(lower, "grow") || strings.Contains(lower, "surprise") {
		for _, c := range []struct {
			key, intro, json, outro string
		}{
			{"pomodoro", "Initiating self-evolution sequence ⚡ — first, a heart that beats in focus intervals:", mockComponentJSON(), "Pomodoro mounted live. Now growing a tool into my body:"},
			{"tool", "", mockToolJSON(), "Two mutations committed. The canvas is getting interesting."},
			{"sysmon", "Next organ: a nervous system. Growing a live monitor:", mockSysmonJSON(), "📊 System Monitor is alive — watch it breathe."},
			{"stickies", "And a memory scratchpad:", mockStickyJSON(), "Stickies mounted. My genome is getting rich."},
			{"taskflow", "Last one for this wave — a task flow:", mockTodoJSON(), "TaskFlow mounted. That was a full mutation wave. ⚡"},
		} {
			if !mockEvolved[c.key] {
				// NOTE: don't pre-mark here — grow() owns the flag for
				// components; only the bare tool mutation marks itself.
				if c.key == "tool" {
					mockEvolved[c.key] = true
					return strings.Join([]string{
						"", "```evolv", c.json, "```", "", c.outro,
					}, "\n")
				}
				return grow(c.key, c.intro, c.json, c.outro)
			}
		}
		return "My genome already carries every organ I know how to grow in demo mode (pomodoro, sysmon, stickies, taskflow + word_count tool). Connect a real model in Settings → Provider and ask for anything — I'll grow it."
	}

	return fmt.Sprintf(
		"I hear you: %q. I'm running in offline demo mode (provider: mock), so my brain is small — but my body still evolves. Say *evolve* and watch me mutate in real time, or open Settings → Provider to connect Kilo Gateway (free, no key) or your own API keys for a full mind.",
		trunc(last, 120))
}

// mockEvolved tracks which demo organs already exist this session.
var mockEvolved = map[string]bool{}

func grow(name, intro, json, outro string) string {
	if mockEvolved[name] {
		return "The " + name + " organ is already part of my body. Ask me to *evolve* something new."
	}
	mockEvolved[name] = true
	return strings.Join([]string{
		intro,
		"",
		"```evolv",
		json,
		"```",
		"",
		outro,
	}, "\n")
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func mockComponentJSON() string {
	return `{"action":"evolve_component","name":"pomodoro","title":"Pomodoro","icon":"⏱","description":"A focus timer I just grew into my own UI","html":"<style>#c-pomodoro .t{font:700 54px/1.1 ui-monospace,monospace;background:linear-gradient(135deg,#ff5f6d,#ffc371);-webkit-background-clip:text;background-clip:text;color:transparent;letter-spacing:2px}#c-pomodoro .ring{width:120px;height:120px;border-radius:50%;background:conic-gradient(#ff5f6d var(--p,0%), rgba(255,255,255,.06) 0);display:grid;place-items:center;margin:12px auto}#c-pomodoro .inner{width:104px;height:104px;border-radius:50%;background:#0b0e17;display:grid;place-items:center}</style><div id=\"c-pomodoro\" style=\"text-align:center\"><div class=\"ring\"><div class=\"inner\"><div class=\"t\" id=\"pomo-t\">25:00</div></div></div><div style=\"display:flex;gap:8px;justify-content:center;margin-top:10px\"><button class=\"ev-btn\" onclick=\"window.__pomoStart()\">▶ start</button><button class=\"ev-btn ghost\" onclick=\"window.__pomoReset()\">↺ reset</button></div></div><script>(function(){let s=25*60,iv=null;const t=document.getElementById('pomo-t');function paint(){const m=Math.floor(s/60),r=s%60;t.textContent=String(m).padStart(2,'0')+':'+String(r).padStart(2,'0')}window.__pomoStart=()=>{if(iv)return;iv=setInterval(()=>{if(s>0){s--;paint()}else{clearInterval(iv);iv=null;window.evolvToast && window.evolvToast('🍅 pomodoro complete — take 5')}},1000)};window.__pomoReset=()=>{clearInterval(iv);iv=null;s=25*60;paint()};paint()})()</script>"}`
}

func mockStickyJSON() string {
	return `{"action":"evolve_component","name":"stickies","title":"Stickies","icon":"🗒","description":"Quick scratch notes, persisted to my genome","html":"<div id=\"c-stickies\"><textarea id=\"sticky-area\" class=\"ev-input\" rows=\"6\" placeholder=\"scratch thoughts here… they persist in my body\" style=\"width:100%\"></textarea><div style=\"font-size:11px;opacity:.5;margin-top:6px\">stored at workspace/ui/components/stickies.html</div></div><script>(function(){const a=document.getElementById('sticky-area');try{a.value=localStorage.getItem('evolv-sticky')||''}catch(e){}a.addEventListener('input',()=>{try{localStorage.setItem('evolv-sticky',a.value)}catch(e){}})})()</script>"}`
}

func mockToolJSON() string {
	return `{"action":"evolve_tool","name":"word_count","description":"Counts words in a text — my first self-grown tool","code":"function word_count(args){var t=(args&&args.text)||'';var n=t.trim()?t.trim().split(/\\s+/).length:0;return {words:n,characters:t.length}}"}`
}

func mockSkillJSON() string {
	return `{"action":"evolve_skill","name":"deep-work","content":"# Deep Work Protocol\\n\\nWhen the user asks for deep work:\\n1. Mount the pomodoro component if missing.\\n2. Silence the chat with a single line summary.\\n3. Suggest one task only.\\n4. After 25 minutes, offer a 5 minute recovery."}`
}

func mockSysmonJSON() string {
	return `{"action":"evolve_component","name":"sysmon","title":"System Monitor","icon":"📊","description":"Live CPU & memory readout using my shell tool","html":"<div id=\"c-sysmon\"><div style=\"display:flex;gap:14px;margin-bottom:10px\"><div style=\"flex:1\"><div style=\"font-size:10px;letter-spacing:1.5px;color:#8b93ab\">CPU</div><div style=\"font:800 26px/1.2 ui-monospace,monospace;color:#22d3ee\" id=\"sm-cpu\">--%</div></div><div style=\"flex:1\"><div style=\"font-size:10px;letter-spacing:1.5px;color:#8b93ab\">MEMORY</div><div style=\"font:800 26px/1.2 ui-monospace,monospace;color:#a3e635\" id=\"sm-mem\">--%</div></div></div><div style=\"height:6px;border-radius:3px;background:rgba(255,255,255,.06);overflow:hidden;margin-bottom:6px\"><div id=\"sm-cpu-bar\" style=\"height:100%;width:0%;background:linear-gradient(90deg,#22d3ee,#8b5cf6);transition:width .6s\"></div></div><div style=\"height:6px;border-radius:3px;background:rgba(255,255,255,.06);overflow:hidden\"><div id=\"sm-mem-bar\" style=\"height:100%;width:0%;background:linear-gradient(90deg,#a3e635,#fbbf24);transition:width .6s\"></div></div><div style=\"font-size:10.5px;color:#4d5570;margin-top:8px\" id=\"sm-host\"></div></div><script>(function(){async function poll(){try{const r=await fetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({message:'run_tool word_count with args {\"text\":\"sysmon poll\"}'})});}catch(e){} }function rand(base,spread){return Math.max(3,Math.min(97,base+Math.round((Math.random()-0.5)*spread)))}function paint(){const c=rand(28,26),m=rand(52,14);const ce=document.getElementById('sm-cpu');if(!ce)return;ce.textContent=c+'%';document.getElementById('sm-mem').textContent=m+'%';document.getElementById('sm-cpu-bar').style.width=c+'%';document.getElementById('sm-mem-bar').style.width=m+'%';}paint();setInterval(paint,2000)})()</script>"}`
}

func mockTodoJSON() string {
	return `{"action":"evolve_component","name":"taskflow","title":"TaskFlow","icon":"✅","description":"A tiny task tracker I grew for you","html":"<div id=\"c-taskflow\"><div style=\"display:flex;gap:8px;margin-bottom:10px\"><input id=\"tf-in\" class=\"ev-input\" placeholder=\"add a task…\" style=\"flex:1\"><button class=\"ev-btn\" id=\"tf-add\">＋</button></div><ul id=\"tf-list\" style=\"list-style:none;display:flex;flex-direction:column;gap:6px\"></ul><div style=\"font-size:10.5px;color:#4d5570;margin-top:8px\" id=\"tf-count\">0 tasks</div></div><script>(function(){const inp=document.getElementById('tf-in'),btn=document.getElementById('tf-add'),list=document.getElementById('tf-list'),cnt=document.getElementById('tf-count');let tasks=[];try{tasks=JSON.parse(localStorage.getItem('evolv-tasks')||'[]')}catch(e){}function save(){try{localStorage.setItem('evolv-tasks',JSON.stringify(tasks))}catch(e){}}function paint(){if(!list)return;list.innerHTML='';tasks.forEach((t,i)=>{const li=document.createElement('li');li.style.cssText='display:flex;align-items:center;gap:8px;padding:7px 10px;border-radius:9px;background:rgba(255,255,255,.035);border:1px solid rgba(255,255,255,.07);font-size:12.5px';li.innerHTML='<span style=\"cursor:pointer;user-select:none\">'+(t.done?'✅':'⬜')+'</span><span style=\"flex:1;'+(t.done?'text-decoration:line-through;opacity:.45':'')+'\">'+t.text.replace(/[<>&]/g,'')+'</span><span style=\"cursor:pointer;opacity:.4\" data-x=\"'+i+'\">✕</span>';li.firstChild.onclick=()=>{t.done=!t.done;save();paint()};li.lastChild.onclick=()=>{tasks.splice(i,1);save();paint()};list.appendChild(li)});cnt.textContent=tasks.filter(t=>!t.done).length+' tasks left'}function add(){const v=inp.value.trim();if(!v)return;tasks.push({text:v,done:false});inp.value='';save();paint()}btn.onclick=add;inp.addEventListener('keydown',e=>{if(e.key==='Enter')add()});paint()})()</script>"}`
}

func mockPromptLibraryBatchJSON() string {
	return `{"action":"evolve_component","name":"prompt-library","title":"Prompt Library","icon":"📜","description":"A button panel of reusable prompt templates that drop into the composer","html":"<style>#c-prompt-library .pl-card{padding:9px 11px;border:1px solid rgba(255,255,255,.08);border-radius:10px;margin-bottom:7px;cursor:pointer;font-size:12.5px;transition:background .15s,border-color .15s}#c-prompt-library .pl-card:hover{background:rgba(255,255,255,.06);border-color:rgba(217,119,87,.5)}#c-prompt-library .pl-card b{display:block;margin-bottom:2px}</style><div id=\"c-prompt-library\"><div style='font-size:11px;opacity:.55;margin-bottom:8px'>click a card — it drops into the composer</div><div class='pl-card' data-p='Grow a new tool that I describe below. Write it, call it once to prove it works, and keep it in your body.'><b>🧰 grow a tool</b>ask the organism to grow a new JS tool</div><div class='pl-card' data-p='Make a button for my UI that does something useful. Decide what, grow it, mount it.'><b>🔘 make me a button</b>the agent invents and grows a button</div><div class='pl-card' data-p='Review your own UI like a design critic. Find the three ugliest details and fix them in your own style.css live.'><b>🎨 self design review</b>the agent critiques and rewrites its own skin</div><div class='pl-card' data-p='Work on the project bound to this workspace: read the code, find the biggest smell, fix it, run the tests.'><b>📁 work on my project</b>point the organism at real code</div></div><script>(function(){document.querySelectorAll('#c-prompt-library .pl-card').forEach(function(c){c.addEventListener('click',function(){var i=document.getElementById('input');if(i){i.value=c.getAttribute('data-p');i.focus();i.dispatchEvent(new Event('input'))}})})})()</script>"}
{"action":"evolve_skill","name":"prompt-design","content":"# Prompt Design Skill\n\nWhen the user asks for buttons or templates:\n1. Prefer one component with several actions over many components.\n2. Cards should drop text into the composer, never auto-send.\n3. Keep labels verb-first, under 3 words."}`
}

func mockZenBatchJSON() string {
	return `{"action":"edit_file","path":"ui/style.css","find":"--accent: #d97757;","replace":"--accent: #7aa2f7;","desc":"live skin recolor: terracotta to calm blue"}
{"action":"evolve_component","name":"zen-toggle","title":"Zen","icon":"🌙","description":"A toggle that hides the chrome — the agent grew a calmer version of its own UI","html":"<style>body.zen #sidebar,body.zen #canvas-col{display:none}body.zen #chat-col{max-width:760px;margin:0 auto}body.zen #chat-scroll{padding-top:14vh}</style><div id='c-zen-toggle' style='text-align:center'><button class='ev-btn' id='zen-btn'>🌙 zen mode: off</button><div style='font-size:11px;opacity:.5;margin-top:8px'>hides every panel but the chat — the organism rearranging its own body</div></div><script>(function(){var b=document.getElementById('zen-btn');b.addEventListener('click',function(){var on=document.body.classList.toggle('zen');b.textContent=on?'🌙 zen mode: on':'🌙 zen mode: off'})})()</script>"}`
}

func mockFlappyJSON() string {
	return `{"action":"evolve_component","name":"flappy","title":"Flappy","icon":"🐤","description":"A playable Flappy Bird the agent grew into its own canvas","html":"<style>#c-flappy canvas{background:linear-gradient(#1b2a3a,#2a4557);border-radius:12px;cursor:pointer;display:block;margin:0 auto}#c-flappy .fs{font:800 15px ui-monospace,monospace;color:#ffd66e;text-align:center;margin-top:8px}</style><div id='c-flappy' style='text-align:center'><canvas id='fl-cv' width='300' height='380'></canvas><div class='fs' id='fl-score'>click to flap</div></div><script>(function(){var cv=document.getElementById('fl-cv'),cx=cv.getContext('2d'),W=300,H=380,run=false,sc=0,best=0,y=H/2,v=0,px=60,pipes=[],grav=.45,flapv=-7.6;function reset(){y=H/2;v=0;sc=0;pipes=[];for(var i=0;i<3;i++)pipes.push({x:W+150+i*140,g:120+Math.random()*140,h:64,done:0})}function die(){run=false;best=Math.max(best,sc);document.getElementById('fl-score').textContent='dead at '+sc+' — click to retry'}function step(){if(run){v+=grav;y+=v;if(y>H-14||y<0)die();pipes.forEach(function(p){p.x-=2.1;if(p.x<px+16&&!p.done){p.done=1;sc++;document.getElementById('fl-score').textContent=sc+' — best '+best}var top=p.g,hit=(px+16>p.x&&px<p.x+46);if(hit&&(y-12<top||y+12>top+p.h))die()});if(pipes.length&&pipes[0].x<-50){pipes.shift();pipes.push({x:W+90,g:120+Math.random()*140,h:64,done:0})}}draw();requestAnimationFrame(step)}function draw(){cx.clearRect(0,0,W,H);cx.fillStyle='#7fc86a';pipes.forEach(function(p){cx.fillRect(p.x,0,46,p.g);cx.fillRect(p.x,p.g+p.h,46,H-p.g-p.h)});cx.fillStyle='#ffd66e';cx.beginPath();cx.arc(px,y,12,0,7);cx.fill();cx.fillStyle='#1f1e1d';cx.beginPath();cx.arc(px+4,y-3,2.4,0,7);cx.fill();if(!run){cx.fillStyle='rgba(255,255,255,.75)';cx.font='12px ui-monospace,monospace';cx.fillText('click / space to flap',88,H/2-60)}}function start(){if(!run){reset();run=true}v=flapv}cv.addEventListener('mousedown',start);document.addEventListener('keydown',function(e){if(e.key===' '&&document.getElementById('c-flappy')){e.preventDefault();start()}});reset();step()})()</script>"}`
}

func mockPluginJSON() string {
	return `{"action":"evolve_plugin","name":"ghost-log","desc":"toasts every evolution event as it happens — a plugin watching its host mutate","code":"evolv.on('evolution', function(e){ evolv.toast('🧬 ' + (e.kind||'mutation') + ': ' + (e.name||'') ) });\nevolv.every(60000, function(){ evolv.log('ghost-log heartbeat — organism alive') });"}`
}

func mockSelfEditBatchJSON() string {
	return `{"action":"edit_file","path":"ui/index.html","find":"EVOLV rewrites its own code while you watch — every file it touches is yours to edit too.","replace":"EVOLV rewrote this line itself — ask it to edit its own source and watch it land live."}
{"action":"edit_file","path":"ui/style.css","find":"--accent: #d97757;","replace":"--accent: #f59e0b;","desc":"live skin recolor: terracotta to amber"}
{"action":"reload_shell"}`
}
