"""Challenge 05: NVIDIA Dynamo on Run:ai, behind BNK. Loaded by build.py (build(h) -> jobs)."""

FLOW_H = 440

FLOW = """<!doctype html><html><head><meta charset="utf-8">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=IBM+Plex+Mono:wght@500&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0}
body{width:1400px;height:%(h)dpx;background:#fff;font-family:Inter,Helvetica,Arial,sans-serif;color:#151a22;padding:24px 30px}
.row{display:flex;gap:12px;align-items:stretch;margin-bottom:12px}
.box{border:1px solid #d6dbe1;border-radius:10px;overflow:hidden;flex:1}
.head{display:flex;justify-content:space-between;align-items:center;padding:8px 14px}
.head b{font-size:14px;letter-spacing:.05em;text-transform:uppercase}
.vm{font:500 12px 'IBM Plex Mono',monospace;border:1px solid currentColor;border-radius:999px;padding:2px 9px}
.cells{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr))}
.cell{padding:10px 14px;border-left:1px solid #e3e6ea}.cell:first-child{border-left:0}
.cell b{display:block;font-size:14px;margin-bottom:2px}.cell span{font-size:12.5px;color:#5d6470}
.client{flex:0 0 190px;border:1.5px solid #6c9aff;border-radius:10px;background:#eef3ff;padding:14px;display:flex;flex-direction:column;justify-content:center}
.client b{font-size:15px}.client span{font:500 12px 'IBM Plex Mono',monospace;color:#3a4f86;margin-top:6px}
.arrow{flex:0 0 34px;display:flex;align-items:center;justify-content:center;font-size:26px;color:#8a929e}
.f5{border:2px solid #e4002b}.f5 .head{background:#e4002b;color:#fff}.f5 .cells{background:#fde8ec}.f5 .cell b{color:#8f0019}
.dyn{border:2px solid #76b900}.dyn .head{background:#76b900;color:#fff}.dyn .cells{background:#eef6e2}.dyn .cell b{color:#2f4a05}
.cs{background:#3a3a3a;border-color:#3a3a3a}.cs .head{color:#fff}.cs .cell{border-color:#555}.cs .cell b{color:#fff}.cs .cell span{color:#c9c9c9}
.gpu{border-color:#76b900}.gpu .head{background:#eef6e2;color:#2f4a05}
.sim{font:500 11px 'IBM Plex Mono',monospace;color:#5d6470;border:1px dashed #8a929e;border-radius:4px;padding:1px 5px;margin-left:5px}
.cs .sim{color:#c9c9c9;border-color:#888}
.bridge{background:#fff6dc;border:1.5px dashed #d9a400;border-radius:10px;padding:9px 14px;margin-bottom:12px;display:flex;justify-content:space-between;align-items:center;font-size:13.5px}
.down{text-align:center;color:#8a929e;font-size:13px;margin:-4px 0 8px}
</style></head><body>
<div class="row">
 <div class="client"><b>Client</b><span>POST /v1/chat/completions<br>&rarr; VIP:8000</span></div>
 <div class="arrow">&rarr;</div>
 <div class="box f5"><div class="head"><b>F5 BNK 2.4 &middot; dynamo</b><span class="vm" style="color:#fff">node: dpu</span></div><div class="cells">
  <div class="cell"><b>Gateway + VIP</b><span>from the Run:ai project</span></div>
  <div class="cell"><b>Rate limit</b><span>iRule, from the GPU quota</span></div>
  <div class="cell"><b>HTTPRoute</b><span>/v1/chat &rarr; Dynamo frontend</span></div></div></div>
 <div class="arrow">&rarr;</div>
 <div class="box dyn"><div class="head"><b>NVIDIA Dynamo</b><span class="vm" style="color:#fff">real</span></div><div class="cells">
  <div class="cell"><b>Frontend</b><span>OpenAI API, tokenizer</span></div>
  <div class="cell"><b>KV-aware router</b><span>picks the worker with the cache</span></div>
  <div class="cell"><b>etcd &middot; NATS</b><span>discovery, KV events</span></div></div></div>
</div>
<div class="bridge"><div><b>BNK &harr; Run:ai tenancy controller</b>&nbsp; project <b>dynamo</b> &rarr; namespace, Gateway, VIP, rate limit</div><span class="vm">node: control</span></div>
<div class="row">
 <div class="box cs"><div class="head"><b>Run:ai scheduler &middot; queue dynamo</b><span class="vm" style="color:#c9c9c9">1 GPU deserved</span></div><div class="cells">
  <div class="cell"><b>kai-scheduler</b><span>places every Dynamo pod</span></div>
  <div class="cell"><b>GPU fractions</b><span>gpu-fraction: 0.5 per worker</span></div>
  <div class="cell"><b>Quota &amp; fair share</b><span>same rules as any project</span></div></div></div>
</div>
<div class="row">
 <div class="box gpu"><div class="head"><b>GPU node &middot; Dynamo workers</b><span class="vm">node: gpu</span></div><div class="cells">
  <div class="cell"><b>Mocker worker A <span class="sim">simulated engine</span></b><span>0.5 &times; A10, KV cache, KV events</span></div>
  <div class="cell"><b>Mocker worker B <span class="sim">simulated engine</span></b><span>0.5 &times; A10, KV cache, KV events</span></div>
  <div class="cell"><b>4 &times; A10 <span class="sim">simulated</span></b><span>NVIDIA fake-gpu-operator</span></div>
  <div class="cell"><b>Optional: disagg</b><span>prefill worker + decode worker</span></div></div></div>
</div>
</body></html>""" % {"h": FLOW_H}


def build(h):
    notes = h.slide(
        "Challenge 5 &middot; NVIDIA Dynamo",
        "Dynamo picks the <em>worker</em>.<br>BNK owns the <b>front door</b>.",
        "NVIDIA Dynamo runs here as one more Run:ai project. Run:ai places its workers on GPU fractions, "
        "BNK gives the project a VIP and a rate limit, and Dynamo's router sends each request to the "
        "worker that already holds its KV cache.",
        [h.card(h.NV, "Dynamo", "Real frontend and router",
                "OpenAI API and KV-aware routing. Workers are Mocker, Dynamo's own simulated engine."),
         h.card(h.BL, "Run:ai", "Schedules every pod",
                "Project dynamo, 1 GPU of quota, two workers on half an A10 each."),
         h.card(h.F5, "BNK 2.4", "Serves the tenant",
                "Same Gateway, VIP and rate limit as any Run:ai project, plus a route to Dynamo.")])
    return {"notes-05-dynamo": (notes, 1600, 900), "05-dynamo-flow": (FLOW, 1400, FLOW_H)}
