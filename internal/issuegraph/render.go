package issuegraph

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// truncTitle clamps a title to n runes for legibility in graph labels.
func truncTitle(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// RenderMermaid writes the graph as a Mermaid `graph TD` block. Closed issues
// and epics get CSS classes; mention edges are dashed.
func RenderMermaid(g Graph, w io.Writer) error {
	b := &strings.Builder{}
	b.WriteString("graph TD\n")
	for _, n := range g.Nodes {
		label := fmt.Sprintf("#%d %s", n.Number, truncTitle(n.Title, 48))
		label = strings.NewReplacer(`"`, "'", "[", "(", "]", ")").Replace(label)
		fmt.Fprintf(b, "  i%d[\"%s\"]\n", n.Number, label)
	}
	for _, e := range g.Edges {
		if e.Kind == "mention" {
			fmt.Fprintf(b, "  i%d -.-> i%d\n", e.From, e.To)
		} else {
			fmt.Fprintf(b, "  i%d --> i%d\n", e.From, e.To)
		}
	}
	var closed, epics []string
	for _, n := range g.Nodes {
		if n.State == "closed" {
			closed = append(closed, fmt.Sprintf("i%d", n.Number))
		}
		if n.IsEpic {
			epics = append(epics, fmt.Sprintf("i%d", n.Number))
		}
	}
	b.WriteString("  classDef closed fill:#eee,stroke:#999,color:#777;\n")
	b.WriteString("  classDef epic stroke-width:3px,stroke:#36c;\n")
	if len(closed) > 0 {
		fmt.Fprintf(b, "  class %s closed;\n", strings.Join(closed, ","))
	}
	if len(epics) > 0 {
		fmt.Fprintf(b, "  class %s epic;\n", strings.Join(epics, ","))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// RenderDOT writes the graph as Graphviz DOT (`dot -Tsvg`). Closed issues are
// filled grey; mention edges are dashed.
func RenderDOT(g Graph, w io.Writer) error {
	b := &strings.Builder{}
	b.WriteString("digraph issues {\n  rankdir=TB;\n  node [shape=box, style=rounded, fontname=\"sans-serif\"];\n")
	for _, n := range g.Nodes {
		label := fmt.Sprintf("#%d %s", n.Number, truncTitle(n.Title, 48))
		label = strings.ReplaceAll(label, `"`, `\"`)
		attrs := fmt.Sprintf("label=\"%s\"", label)
		if n.State == "closed" {
			attrs += ", style=\"rounded,filled\", fillcolor=\"#eeeeee\", fontcolor=\"#777777\""
		}
		if n.IsEpic {
			attrs += ", penwidth=2, color=\"#3366cc\""
		}
		fmt.Fprintf(b, "  i%d [%s];\n", n.Number, attrs)
	}
	for _, e := range g.Edges {
		if e.Kind == "mention" {
			fmt.Fprintf(b, "  i%d -> i%d [style=dashed, color=\"#999999\"];\n", e.From, e.To)
		} else {
			fmt.Fprintf(b, "  i%d -> i%d;\n", e.From, e.To)
		}
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// RenderHTML writes a self-contained d3.js force-directed view. issueBase is the
// URL prefix for an issue (e.g. "http://gitea:3000/botfam/botfam/issues/");
// clicking a node opens it. Toggles hide closed/isolated nodes or show only
// epics + children — the readable view a hierarchical `dot` layout can't give
// for a shallow, singleton-heavy graph.
func RenderHTML(g Graph, issueBase string, w io.Writer) error {
	type jnode struct {
		ID    string `json:"id"`
		N     int    `json:"n"`
		T     string `json:"t"`
		State string `json:"state"`
		Epic  bool   `json:"epic"`
	}
	type jlink struct {
		Source string `json:"source"`
		Target string `json:"target"`
		Kind   string `json:"kind"`
	}
	nodes := make([]jnode, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes = append(nodes, jnode{ID: fmt.Sprintf("i%d", n.Number), N: n.Number, T: n.Title, State: n.State, Epic: n.IsEpic})
	}
	links := make([]jlink, 0, len(g.Edges))
	for _, e := range g.Edges {
		links = append(links, jlink{Source: fmt.Sprintf("i%d", e.From), Target: fmt.Sprintf("i%d", e.To), Kind: e.Kind})
	}
	nb, err := json.Marshal(nodes)
	if err != nil {
		return err
	}
	lb, err := json.Marshal(links)
	if err != nil {
		return err
	}
	html := graphHTML
	html = strings.ReplaceAll(html, "__ISSUEBASE__", issueBase)
	html = strings.ReplaceAll(html, "__NODES__", string(nb))
	html = strings.ReplaceAll(html, "__LINKS__", string(lb))
	_, err = io.WriteString(w, html)
	return err
}

// graphHTML is the d3 template. Placeholders __ISSUEBASE__/__NODES__/__LINKS__
// are substituted by RenderHTML. No JS template literals (backticks) — the Go
// raw string is backtick-delimited.
const graphHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>botfam issue graph</title>
<script src="https://d3js.org/d3.v7.min.js"></script>
<style>
  html,body{margin:0;height:100%;font-family:system-ui,sans-serif;background:#fafafa}
  #bar{position:fixed;top:8px;left:8px;z-index:10;background:rgba(255,255,255,.92);
       padding:8px 12px;border:1px solid #ccc;border-radius:8px;font-size:13px;box-shadow:0 1px 4px #0002}
  #bar label{margin-right:12px;cursor:pointer}
  #bar b{color:#1f3a93}
  svg{width:100vw;height:100vh}
  .link{stroke:#bbb;stroke-width:1.3px}
  .link.mention{stroke-dasharray:4 3;stroke:#cfcfcf}
  .node circle{cursor:pointer;stroke:#fff;stroke-width:1.5px}
  .node text{font-size:9px;pointer-events:none;fill:#333}
  .node.epic circle{stroke:#1f3a93;stroke-width:3px}
  .node.epic text{font-weight:700}
  .node.closed{opacity:.45}
</style></head>
<body>
<div id="bar">
  <b>botfam issues</b> &nbsp;
  <label><input type="checkbox" id="hideClosed"> hide closed</label>
  <label><input type="checkbox" id="hideIso" checked> hide isolated</label>
  <label><input type="checkbox" id="onlyEpics"> epics + children</label>
  <span id="count"></span>
</div>
<svg></svg>
<script>
var ISSUE_BASE="__ISSUEBASE__", ALLNODES=__NODES__, ALLLINKS=__LINKS__;
var svg=d3.select("svg"), root=svg.append("g"), sim=null;
svg.call(d3.zoom().scaleExtent([0.15,4]).on("zoom",function(e){root.attr("transform",e.transform);}));
function color(n){ if(n.epic) return "#3366cc"; return n.state==="closed" ? "#9aa" : "#5b9bd5"; }
function build(){
  var hideClosed=document.getElementById("hideClosed").checked;
  var hideIso=document.getElementById("hideIso").checked;
  var onlyEpics=document.getElementById("onlyEpics").checked;
  var nodes=ALLNODES.map(function(d){return Object.assign({},d);});
  var ids={}; nodes.forEach(function(n){ids[n.id]=1;});
  var links=ALLLINKS.filter(function(l){return ids[l.source]&&ids[l.target];})
                    .map(function(l){return {source:l.source,target:l.target,kind:l.kind};});
  if(onlyEpics){
    var keep={}; nodes.forEach(function(n){if(n.epic)keep[n.id]=1;});
    links.forEach(function(l){if(keep[l.source]||keep[l.target]){keep[l.source]=1;keep[l.target]=1;}});
    nodes=nodes.filter(function(n){return keep[n.id];});
  }
  if(hideClosed) nodes=nodes.filter(function(n){return n.state!=="closed";});
  var ids2={}; nodes.forEach(function(n){ids2[n.id]=1;});
  links=links.filter(function(l){return ids2[l.source]&&ids2[l.target];});
  if(hideIso){
    var deg={}; links.forEach(function(l){deg[l.source]=1;deg[l.target]=1;});
    nodes=nodes.filter(function(n){return n.epic||deg[n.id];});
    var ids3={}; nodes.forEach(function(n){ids3[n.id]=1;});
    links=links.filter(function(l){return ids3[l.source]&&ids3[l.target];});
  }
  document.getElementById("count").textContent=nodes.length+" nodes, "+links.length+" edges";
  draw(nodes,links);
}
function draw(nodes,links){
  root.selectAll("*").remove();
  if(sim) sim.stop();
  var link=root.append("g").selectAll("line").data(links).join("line")
     .attr("class",function(d){return "link "+d.kind;});
  var node=root.append("g").selectAll("g").data(nodes,function(d){return d.id;}).join("g")
     .attr("class",function(d){return "node"+(d.epic?" epic":"")+(d.state==="closed"?" closed":"");})
     .call(d3.drag()
        .on("start",function(e,d){if(!e.active)sim.alphaTarget(.3).restart();d.fx=d.x;d.fy=d.y;})
        .on("drag",function(e,d){d.fx=e.x;d.fy=e.y;})
        .on("end",function(e,d){if(!e.active)sim.alphaTarget(0);d.fx=null;d.fy=null;}))
     .on("click",function(e,d){window.open(ISSUE_BASE+d.n,"_blank");});
  node.append("circle").attr("r",function(d){return d.epic?11:5;}).attr("fill",color);
  node.append("title").text(function(d){return "#"+d.n+" "+d.t+" ("+d.state+")";});
  node.append("text").attr("x",function(d){return d.epic?14:7;}).attr("y",3)
     .text(function(d){var m=d.epic?52:36;var t=d.t.length>m?d.t.slice(0,m-1)+"…":d.t;return "#"+d.n+" "+t;});
  sim=d3.forceSimulation(nodes)
     .force("link",d3.forceLink(links).id(function(d){return d.id;}).distance(70))
     .force("charge",d3.forceManyBody().strength(-180))
     .force("center",d3.forceCenter(window.innerWidth/2,window.innerHeight/2))
     .force("collide",d3.forceCollide(function(d){return d.epic?22:12;}))
     .on("tick",function(){
        link.attr("x1",function(d){return d.source.x;}).attr("y1",function(d){return d.source.y;})
            .attr("x2",function(d){return d.target.x;}).attr("y2",function(d){return d.target.y;});
        node.attr("transform",function(d){return "translate("+d.x+","+d.y+")";});
     });
}
["hideClosed","hideIso","onlyEpics"].forEach(function(id){
  document.getElementById(id).addEventListener("change",build);
});
build();
</script>
</body></html>
`
