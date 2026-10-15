---
slug: build
id: qxqyvyqyfkya
type: challenge
title: Build and publish Run:ai images
teaser: Wait for the build to finish.
tabs:
- id: u5cm0evls8bn
  title: Builder
  type: terminal
  hostname: builder
difficulty: basic
timelimit: 14400
enhanced_loading: null
---
The build runs in the background. Follow it with:

```bash
tail -f /root/build/run.log
```

Status is in `/root/build/status` (`running`, `done`, `failed`). Click **Check** when it says `done`.
