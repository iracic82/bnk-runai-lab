"""Builds the loading-note slides and assignment diagrams, rendered to ../assets/*.png.

Run:  python3 notes-src/build.py   (needs: pip install playwright && playwright install chromium)
"""
import asyncio
import pathlib

OUT = pathlib.Path(__file__).parent
ASSETS = OUT.parent / "assets"

STYLE = """
*{box-sizing:border-box;margin:0}
body{width:1600px;height:900px;overflow:hidden;font-family:Inter,Helvetica,Arial,sans-serif;color:#eef1e8;
 background:radial-gradient(1100px 650px at 88% -12%,#22351a 0,transparent 60%),
            radial-gradient(900px 600px at -12% 112%,#3a0f18 0,transparent 55%),#0b0d0b;}
.bar{position:absolute;left:0;top:0;width:100%;height:8px;background:linear-gradient(90deg,#76b900 0 50%,#e4002b 50%)}
.wrap{position:absolute;inset:0;padding:78px 100px 90px;display:flex;flex-direction:column}
.eyebrow{font-size:22px;letter-spacing:.14em;text-transform:uppercase;color:#98a08f;font-weight:500}
h1{font-size:62px;line-height:1.06;font-weight:700;letter-spacing:-.02em;margin-top:18px;max-width:1360px}
h1 em{font-style:normal;color:#9ccc4a}
h1 b{font-weight:700;color:#ff5a6e}
.sub{font-size:27px;line-height:1.42;color:#c3c8bb;margin-top:20px;max-width:1280px}
.cards{display:grid;gap:22px;margin-top:48px;grid-template-columns:repeat(3,1fr)}
.card{background:rgba(24,27,23,.88);border:1px solid #2c3229;border-radius:16px;padding:28px 28px 30px;position:relative;overflow:hidden}
.card:before{content:"";position:absolute;left:0;top:0;bottom:0;width:5px;background:var(--a)}
.card .k{font-size:16px;color:#98a08f;letter-spacing:.08em;text-transform:uppercase;font-weight:600}
.card h3{font-size:30px;font-weight:600;margin-top:12px;line-height:1.15}
.card p{font-size:20px;line-height:1.45;color:#c3c8bb;margin-top:10px}
.foot{position:absolute;right:100px;bottom:30px;font-size:16px;color:#6c7365}
"""
NV, F5, BL, AM = "#76b900", "#e4002b", "#6c9aff", "#e0b54a"


def page(body: str) -> str:
    return (f'<!doctype html><html><head><meta charset="utf-8">'
            f'<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">'
            f'<style>{STYLE}</style></head><body><div class="bar"></div><div class="wrap">{body}</div>'
            f'<div class="foot">F5 BIG-IP Next for Kubernetes 2.4 &middot; open-source NVIDIA Run:ai</div></body></html>')


def card(color, key, title, text):
    return f'<div class="card" style="--a:{color}"><div class="k">{key}</div><h3>{title}</h3><p>{text}</p></div>'


def slide(eyebrow, title, sub, cards):
    return page(f'<div class="eyebrow">{eyebrow}</div><h1>{title}</h1><p class="sub">{sub}</p>'
                f'<div class="cards">{"".join(cards)}</div>')


SLIDES = {
    "notes-01-hero": slide(
        "BNK 2.4 &times; open-source NVIDIA Run:ai",
        "Define a tenant <em>once, in Run:ai</em>.<br>BNK <b>serves, limits and meters</b> it.",
        "NVIDIA just open-sourced Run:ai, the scheduler that decides who gets GPUs. F5 BIG-IP Next for "
        "Kubernetes decides how requests reach them. A small open-source controller connects the two, so "
        "a project created in Run:ai exists in BNK seconds later.",
        [card(NV, "Run:ai", "Owns the GPU", "Projects, quotas, GPU fractions, priority and preemption."),
         card(F5, "BNK 2.4", "Owns the request", "Front door per tenant, rate limit from GPU quota, model-aware routing, token metering."),
         card(AM, "The glue", "Open source", "Reads Run:ai projects, writes BNK config. Delete the project and BNK forgets it.")]),
    "notes-01-real": slide(
        "What is real in this lab",
        "Real software. <em>Simulated hardware.</em>",
        "Everything you touch is the real product: BNK 2.4 and the open-source Run:ai control plane and "
        "scheduler, on a four-node Kubernetes cluster. Only the hardware is simulated, and the lab says "
        "where.",
        [card(NV, "GPUs", "fake-gpu-operator", "NVIDIA's own GPU simulator: 4 &times; A10 on the gpu node, scheduled by Run:ai for real."),
         card(BL, "Models", "vLLM simulator", "OpenAI-compatible, real vLLM metrics, latency that grows with load."),
         card(AM, "DPU", "Simulated BlueField", "A separate node with a tunnel to the GPU node. The path is real; offload is not measured here.")]),
}

ARCH = """<!doctype html><html><head><meta charset="utf-8">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=IBM+Plex+Mono:wght@500&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0}
body{width:1400px;height:530px;background:#fff;font-family:Inter,Helvetica,Arial,sans-serif;color:#151a22;padding:26px 30px}
.layer{border:1px solid #d6dbe1;border-radius:10px;overflow:hidden;margin-bottom:10px}
.head{display:flex;justify-content:space-between;align-items:center;padding:8px 14px}
.head b{font-size:14px;letter-spacing:.05em;text-transform:uppercase}
.vm{font:500 12px 'IBM Plex Mono',monospace;border:1px solid currentColor;border-radius:999px;padding:2px 9px}
.cells{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr))}
.cell{padding:10px 14px;border-left:1px solid #e3e6ea}.cell:first-child{border-left:0}
.cell b{display:block;font-size:14px;margin-bottom:2px}.cell span{font-size:12.5px;color:#5d6470}
.cp{border-color:#76b900}.cp .head{background:#a9d46b}.cp .cells{background:#eef6e2}
.f5{border:2px solid #e4002b}.f5 .head{background:#e4002b;color:#fff}.f5 .cells{background:#fde8ec}.f5 .cell b{color:#8f0019}
.cs{background:#3a3a3a;border-color:#3a3a3a}.cs .head{color:#fff}.cs .cell{border-color:#555}.cs .cell b{color:#fff}.cs .cell span{color:#c9c9c9}
.gpu{border-color:#76b900}.gpu .head{background:#eef6e2;color:#2f4a05}
.bridge{background:#fff6dc;border:1.5px dashed #d9a400;border-radius:10px;padding:9px 14px;margin-bottom:10px;display:flex;justify-content:space-between;align-items:center;font-size:13.5px}
.sim{font:500 11px 'IBM Plex Mono',monospace;color:#5d6470;border:1px dashed #8a929e;border-radius:4px;padding:1px 5px;margin-left:5px}
.new{font:600 10px 'IBM Plex Mono',monospace;background:#e4002b;color:#fff;border-radius:4px;padding:2px 6px;margin-left:6px}
</style></head><body>
<div class="layer cp"><div class="head"><b>Run:ai control plane</b><span class="vm">node: runai</span></div><div class="cells">
<div class="cell"><b>Projects &amp; departments</b><span>the tenant is defined here, once</span></div>
<div class="cell"><b>Quotas &amp; priority</b><span>GPU share per project</span></div>
<div class="cell"><b>Workloads</b><span>deploy and track inference</span></div>
<div class="cell"><b>UI &middot; CLI &middot; API</b><span>what admins and ML engineers use</span></div></div></div>
<div class="bridge"><div><b>BNK &harr; Run:ai tenancy controller</b><span class="new">OPEN SOURCE</span>&nbsp; reads every Run:ai project, writes its BNK config</div><span class="vm">node: control</span></div>
<div class="layer f5"><div class="head"><b>F5 BNK 2.4 &middot; AI factory service plane</b><span class="vm" style="color:#fff">node: dpu <span style="opacity:.8">(BlueField simulated)</span></span></div><div class="cells">
<div class="cell"><b>Front door per tenant</b><span>one hostname per Run:ai project</span></div>
<div class="cell"><b>Rate limit</b><span>from the project's GPU quota</span></div>
<div class="cell"><b>Endpoint Picker</b><span>best replica, by KV cache and queue</span></div>
<div class="cell"><b>Metering</b><span>tokens per project</span></div></div></div>
<div class="layer cs"><div class="head"><b>Run:ai cluster services</b><span class="vm" style="color:#c9c9c9">node: gpu</span></div><div class="cells">
<div class="cell"><b>Run:ai Scheduler</b><span>fair share, preemption, GPU fractions</span></div>
<div class="cell"><b>Resource management</b><span>project &rarr; namespace</span></div>
<div class="cell"><b>Operators</b><span>reconcile projects &amp; workloads</span></div></div></div>
<div class="layer gpu"><div class="head"><b>GPU infrastructure</b><span class="vm">node: gpu</span></div><div class="cells">
<div class="cell"><b>4 &times; A10 <span class="sim">simulated</span></b><span>NVIDIA fake-gpu-operator</span></div>
<div class="cell"><b>Model servers <span class="sim">simulated</span></b><span>vLLM simulator, 0.5 GPU each</span></div>
<div class="cell"><b>DPU &rarr; GPU link</b><span>gretap "pcie" tunnel</span></div></div></div>
</body></html>"""


async def render() -> None:
    from playwright.async_api import async_playwright
    ASSETS.mkdir(exist_ok=True)
    jobs = {name: (html, 1600, 900) for name, html in SLIDES.items()}
    jobs["01-architecture"] = (ARCH, 1400, 530)
    async with async_playwright() as p:
        browser = await p.chromium.launch(channel="chrome")  # installed Chrome; no browser download
        for name, (html, w, h) in jobs.items():
            (OUT / f"{name}.html").write_text(html)
            pg = await browser.new_page(viewport={"width": w, "height": h})
            await pg.set_content(html, wait_until="networkidle")
            await pg.screenshot(path=str(ASSETS / f"{name}.png"))
            await pg.close()
            print(f"assets/{name}.png")
        await browser.close()


if __name__ == "__main__":
    asyncio.run(render())
