# Contract: labctl (learner + check-script CLI)

These commands are cwd-independent, exit 0 on success, and with `--json` print
machine-readable output for check scripts.

| Command | Purpose |
|---|---|
| `labctl status` | health of BNK, KAI, fake GPUs, sim, controller |
| `labctl tenant create <name> --gpu <n> [--priority <p>]` | create a KAI Project (thin wrapper over kubectl apply) |
| `labctl tenant delete <name>` | delete the KAI Project |
| `labctl tenant show <name>` | Run:ai side and generated BNK bundle side by side |
| `labctl model deploy <tenant> <model> [--replicas N] [--fraction F] [--version v]` | sim model server scheduled by KAI |
| `labctl load <profile> [--tenant T] [--seconds 30]` | profiles: `steady`, `prefix-heavy`, `overload`, `rollout` |
| `labctl routing <tenant> rr\|epp` | toggle baseline round-robin vs Endpoint Picker (C3 comparison) |
| `labctl dash [panel]` | print the Grafana URL for the Instruqt tab |
| `labctl verify <challenge>` | same logic as the Instruqt check script |
