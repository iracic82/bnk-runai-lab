"""Notes slides and white diagrams for challenges 02, 03, 04 and 06.

Picked up by build.py (extra_jobs): build(h) returns {asset_name: (html, width, height)}.
"""

FONTS = ('<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700'
         '&family=IBM+Plex+Mono:wght@500&display=swap" rel="stylesheet">')

# White diagram style, matching ARCH in build.py.
DIAG_CSS = """
*{box-sizing:border-box;margin:0}
body{background:#fff;font-family:Inter,Helvetica,Arial,sans-serif;color:#151a22;padding:26px 30px}
.title{font-size:15px;letter-spacing:.06em;text-transform:uppercase;color:#5d6470;font-weight:600;margin-bottom:14px}
.row{display:flex;align-items:stretch;gap:0}
.col{display:flex;flex-direction:column;gap:10px}
.box{border:1.5px solid #d6dbe1;border-radius:10px;background:#fff;overflow:hidden}
.box .hd{padding:7px 12px;font-size:12.5px;font-weight:700;letter-spacing:.05em;text-transform:uppercase;display:flex;justify-content:space-between;align-items:center;gap:8px}
.box .bd{padding:9px 12px;font-size:13px;line-height:1.45;color:#2a313b}
.box .bd b{color:#151a22}
.nv{border-color:#76b900}.nv .hd{background:#a9d46b;color:#1d2e03}.nv .bd{background:#f4f9ec}
.f5{border:2px solid #e4002b}.f5 .hd{background:#e4002b;color:#fff}.f5 .bd{background:#fdf0f2}
.am{border:1.5px dashed #d9a400}.am .hd{background:#fff1c4;color:#5a4300}.am .bd{background:#fffaeb}
.bl{border-color:#6c9aff}.bl .hd{background:#dfe8ff;color:#1c3b8a}.bl .bd{background:#f5f8ff}
.gy{border-color:#3a3a3a}.gy .hd{background:#3a3a3a;color:#fff}.gy .bd{background:#f3f3f3}
.vm{font:500 11px 'IBM Plex Mono',monospace;border:1px solid currentColor;border-radius:999px;padding:1px 8px;text-transform:none;letter-spacing:0;font-weight:500}
.mono{font:500 12px 'IBM Plex Mono',monospace}
.num{font-weight:700;color:#c4001f}
.ok{font-weight:700;color:#3d6b00}
.sim{font:500 10.5px 'IBM Plex Mono',monospace;color:#5d6470;border:1px dashed #8a929e;border-radius:4px;padding:0 5px;margin-left:4px}
.arrow{display:flex;flex-direction:column;align-items:center;justify-content:center;min-width:92px;padding:0 6px;text-align:center}
.arrow .ln{font-size:30px;line-height:1;color:#8a929e}
.arrow .lb{font-size:11.5px;color:#5d6470;margin-top:4px;line-height:1.3}
.arrow.red .ln{color:#e4002b}.arrow.red .lb{color:#c4001f;font-weight:600}
.arrow.grn .ln{color:#76b900}.arrow.grn .lb{color:#3d6b00;font-weight:600}
.chip{display:inline-block;font:500 11.5px 'IBM Plex Mono',monospace;background:#fff;border:1px solid #d6dbe1;border-radius:6px;padding:2px 7px;margin:2px 3px 2px 0}
.f5 .chip{border-color:#f2a9b5}
.legend{margin-top:12px;font-size:12px;color:#5d6470}
"""


def diag(title: str, body: str, width: int) -> str:
    return (f'<!doctype html><html><head><meta charset="utf-8">{FONTS}<style>{DIAG_CSS}'
            f'body{{width:{width}px}}</style></head><body><div class="title">{title}</div>{body}</body></html>')


def box(kind, head, body, tag="", style=""):
    t = f'<span class="vm">{tag}</span>' if tag else ""
    return f'<div class="box {kind}" style="{style}"><div class="hd"><span>{head}</span>{t}</div><div class="bd">{body}</div></div>'


def arrow(label, cls="", glyph="&rarr;"):
    return f'<div class="arrow {cls}"><div class="ln">{glyph}</div><div class="lb">{label}</div></div>'


# ---------- 02: tenant defined once ----------

def tenant_flow() -> str:
    runai = '<div class="col" style="width:300px">' + box(
        "nv", "Run:ai project tenant-a", "department <b>acme</b><br>GPU quota <b>2</b><br>priority <b>150</b>",
        "kai.resources") + box(
        "nv", "Run:ai project tenant-b", "department <b>default</b><br>GPU quota <b>0.5</b><br>priority <b>50</b>",
        "kai.resources") + '</div>'
    ctrl = '<div class="col" style="width:300px;justify-content:center">' + box(
        "am", "Tenancy controller",
        "watches every Run:ai project<br>"
        "<span class='mono'>rate = 20 req/s &times; max(GPU, 0.25)</span><br>"
        "<span class='mono'>priority &ge; 100 &rarr; Critical</span><br>"
        "<span class='mono'>priority &lt; 100 &rarr; Sheddable</span><br>"
        "project deleted &rarr; BNK objects deleted", "node: control") + '</div>'

    def bnk(name, rps, crit):
        return box("f5", f"BNK tenant {name}",
                   f'<span class="chip">Gateway tenant-{name}</span><span class="chip">VIP :8000</span><br>'
                   f'<span class="chip">HTTPRoute</span><span class="chip">InferencePool</span><span class="chip">F5EPP</span><br>'
                   f'<span class="chip">iRule <span class="num">{rps} req/s</span></span><span class="chip">NetPolicy</span>'
                   f'&nbsp;<b>{crit}</b>', "f5-bnk + tenant ns")
    out = '<div class="col" style="width:470px">' + bnk("tenant-a", 40, "Critical") + bnk("tenant-b", 10, "Sheddable") + '</div>'
    body = ('<div class="row">' + runai + arrow("watch<br>Project + Queue") + ctrl
            + arrow("server-side<br>apply") + out + '</div>'
            '<div class="legend">tenant-a: 20 &times; 2 = 40 req/s. tenant-b: 20 &times; 0.5 = 10 req/s. '
            'Change tenant-b to 1 GPU and its iRule becomes 20 req/s. Every object carries the label '
            '<span class="mono">tenancy.f5.com/project</span>.</div>')
    return diag("Define the tenant once in Run:ai. The controller writes BNK.", body, 1400)


# ---------- 03: isolation before the GPU ----------

def isolation() -> str:
    clients = '<div class="col" style="width:230px">' + box(
        "bl", "Client of tenant-a", "steady load<br>within 40 req/s") + box(
        "bl", "Client of tenant-b", "overload<br>far above 10 req/s") + '</div>'
    mid = ('<div class="col" style="width:110px;justify-content:space-around">'
           + arrow("all requests", "grn") + arrow("over 10 req/s:<br>429 sent back", "red", "&larr;")
           + '</div>')
    edge = '<div class="col" style="width:400px;justify-content:center">' + box(
        "f5", "BNK edge &middot; TMM",
        "one Gateway and VIP per Run:ai project<br>"
        "<b>iRule per tenant</b>: counts requests per second<br>"
        "&nbsp;&nbsp;tenant-a limit <span class='num'>40</span> &middot; tenant-b limit <span class='num'>10</span><br>"
        "over the limit &rarr; <b>429</b> with JSON body,<br>answered here, before any GPU<br>"
        "<b>Endpoint Picker</b> (F5EPP) picks a replica<br>by queue depth and KV-cache prefix",
        "node: dpu") + '</div>'
    mid2 = ('<div class="col" style="width:110px;justify-content:space-around">'
            + arrow("tenant-a<br>within share", "grn") + arrow("tenant-b<br>within share", "grn") + '</div>')
    gpu = '<div class="col" style="width:440px;justify-content:center">' + box(
        "nv", "GPU node &middot; one GPU, two tenants",
        '<div style="display:flex;gap:8px;margin:4px 0 6px">'
        '<div style="flex:1;border:1.5px solid #76b900;border-radius:6px;padding:6px;text-align:center;background:#fff">'
        'tenant-a model<br><span class="mono">gpu-fraction 0.5</span></div>'
        '<div style="flex:1;border:1.5px solid #76b900;border-radius:6px;padding:6px;text-align:center;background:#fff">'
        'tenant-b model<br><span class="mono">gpu-fraction 0.5</span></div></div>'
        'Placed by the Run:ai scheduler.<span class="sim">simulated GPU</span>',
        "node: gpu") + '</div>'
    body = ('<div class="row">' + clients + mid + edge + mid2 + gpu + '</div>'
            '<div class="legend">Each tenant keeps its share. tenant-b\'s excess is refused at the edge, so the '
            'GPU it shares with tenant-a only ever sees tenant-b\'s fair share of work.</div>')
    return diag("Isolation happens at the BNK edge, before the GPU", body, 1400)


# ---------- 04: metering ----------

def metering() -> str:
    req = '<div class="col" style="width:210px;justify-content:center">' + box(
        "bl", "Chat request", "to tenant-a's VIP<br>model answers with<br><span class='mono'>usage.prompt_tokens</span><br>"
        "<span class='mono'>usage.completion_tokens</span>") + '</div>'
    edge = '<div class="col" style="width:270px;justify-content:center">' + box(
        "f5", "BNK edge &middot; iRule", "reads the <b>usage</b> block of each response<br>"
        "tags it with the Run:ai project and department", "node: dpu") + '</div>'
    meter = '<div class="col" style="width:300px;justify-content:center">' + box(
        "am", "Token meter", "<span class='mono'>bnk_tokens_total</span><br>"
        "<span class='mono'>{project, department,<br>&nbsp;model, direction}</span><br>"
        "<span class='mono'>bnk_requests_total{..., code}</span>", "node: control") + '</div>'
    outs = '<div class="col" style="width:330px">' + box(
        "gy", "Prometheus + Grafana", "dashboard <b>One tenant, two planes</b><br>tokens per project, per department, per GPU",
        "node: control") + box(
        "nv", "Write-back to Run:ai", "Project tenant-a<br><span class='mono'>tenancy.f5.com/tokens-total</span><br>"
        "Department acme: sum of its projects", "kai.resources") + '</div>'
    body = ('<div class="row">' + req + arrow("response") + edge + arrow("counts") + meter
            + arrow("scrape<br>+ annotate") + outs + '</div>'
            '<div class="legend">Tokens are counted where every request already passes: the BNK edge. Run:ai sees the '
            'result on its own objects, so GPU share and token use sit side by side. Token counts here come from the '
            'vLLM simulator, so the numbers are synthetic; the path is real.</div>')
    return diag("Tokens metered per Run:ai project, at the edge", body, 1400)


# ---------- 06: recap ----------

def recap() -> str:
    left = '<div class="col" style="width:640px">' + box(
        "nv", "Run:ai &middot; owns the GPU", "Projects, departments, quotas, priority, GPU fractions, scheduling",
        "node: runai + gpu") + box(
        "am", "Tenancy controller + token meter &middot; open source", "Project &rarr; Gateway, route, pool, picker, "
        "rate limit, priority. Tokens &rarr; Prometheus and back onto the Project", "node: control") + box(
        "f5", "BNK 2.4 &middot; owns the request", "Front door per tenant, 429 before the GPU, Endpoint Picker, "
        "token metering at the edge", "node: dpu (simulated)") + box(
        "gy", "GPUs and models", "4 &times; A10 from fake-gpu-operator, vLLM simulator", "node: gpu") + '</div>'
    rows = [
        ("Real", "#3d6b00", "BNK 2.4 data and control plane, Run:ai control plane and scheduler, "
         "Kubernetes, the controller, the 429s, the Endpoint Picker, the metrics path"),
        ("Simulated", "#5d6470", "GPUs (fake-gpu-operator), model servers (vLLM simulator, synthetic tokens), "
         "the BlueField DPU (a separate node with a tunnel)"),
        ("Design intent", "#c4001f", "On a BlueField-3, TMM runs on the DPU and frees host CPU for the GPU "
         "server. Not measured in this lab."),
    ]
    tbl = "".join(f'<div style="border-left:5px solid {c};padding:8px 12px;margin-bottom:10px;background:#f7f8fa;'
                  f'border-radius:0 8px 8px 0"><div style="font-weight:700;color:{c};font-size:13px;'
                  f'text-transform:uppercase;letter-spacing:.05em">{k}</div><div style="font-size:13.5px;'
                  f'line-height:1.45;margin-top:3px">{v}</div></div>' for k, c, v in rows)
    right = f'<div class="col" style="width:640px;margin-left:60px">{tbl}</div>'
    return diag("What you built", '<div class="row">' + left + right + '</div>', 1400)


def build(h) -> dict:
    s = h.slide
    c = h.card
    return {
        "notes-02": (s(
            "Challenge 2 &middot; Tenant defined once",
            "Create a project in Run:ai.<br><em>BNK builds its front door.</em>",
            "You will create two Run:ai projects with different GPU quotas and priorities, deploy a model "
            "for each on half of one GPU, and watch BNK tenants appear without touching BNK.",
            [c(h.NV, "Run:ai", "tenant-a and tenant-b", "2 GPU, priority 150. And 0.5 GPU, priority 50."),
             c(h.AM, "Controller", "Quota becomes a limit", "20 req/s per GPU: 40 for tenant-a, 10 for tenant-b."),
             c(h.F5, "BNK", "Delete it, it's gone", "Remove a project and BNK removes its Gateway, route and limit.")]), 1600, 900),
        "notes-03": (s(
            "Challenge 3 &middot; Isolation before the GPU",
            "Each tenant <em>keeps its share</em>.<br>The excess is <b>refused at the edge</b>.",
            "tenant-b floods its endpoint while tenant-a runs normal traffic. BNK answers tenant-b's excess "
            "with 429 before it reaches the GPU the two tenants share.",
            [c(h.F5, "Rate limit", "429 at the edge", "A JSON error naming the Run:ai project. No GPU work done."),
             c(h.NV, "Shared GPU", "tenant-a unaffected", "Its latency stays flat while tenant-b is throttled."),
             c(h.BL, "Gateway API", "No cross-tenant routes", "A route from tenant-b can't attach to tenant-a's Gateway.")]), 1600, 900),
        "notes-04": (s(
            "Challenge 4 &middot; Tokens per project",
            "Who used the tokens?<br><em>Answered per Run:ai project.</em>",
            "BNK counts tokens where every request passes anyway. The counts land in Grafana next to GPU "
            "share, and are written back onto the Run:ai project and its department.",
            [c(h.F5, "Edge", "Counted by BNK", "From the usage block each model response already carries."),
             c(h.AM, "Meter", "Prometheus metrics", "bnk_tokens_total by project, department, model, direction."),
             c(h.NV, "Run:ai", "Written back", "tokens-total on the Project, rolled up into the Department.")]), 1600, 900),
        "notes-06": (s(
            "Challenge 6 &middot; Debrief",
            "What you built, <em>and what's next</em>.",
            "One tenant model for the AI factory: Run:ai decides who gets GPUs, BNK decides how requests "
            "reach them, and an open-source controller keeps the two in step.",
            [c(h.NV, "Real", "Real software", "BNK 2.4 and open-source Run:ai, on a real Kubernetes cluster."),
             c(h.BL, "Simulated", "Simulated hardware", "GPUs, model servers and the DPU. The lab says where."),
             c(h.F5, "Next", "BlueField-3", "TMM on the DPU, host CPU freed. Design intent, not measured here.")]), 1600, 900),
        "02-tenant-flow": (tenant_flow(), 1400, 372),
        "03-isolation": (isolation(), 1400, 300),
        "04-metering": (metering(), 1400, 318),
        "06-recap": (recap(), 1400, 410),
    }
