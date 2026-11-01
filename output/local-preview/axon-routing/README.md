# 本地前端预览

不需要部署线上。这里有两个入口：

- 静态效果页：<http://127.0.0.1:5174/preview.html>，直接切换四处改动的真实截图。
- 可交互前端：<http://127.0.0.1:5173/models>，运行当前源码，支持 Vite 热更新。

本地预览首次启动会自动初始化演示账号和数据。账号沿用仓库 E2E 测试约定：`my@example.com`，密码 `pwd123456`。
这组公开测试值仅用于本机演示，不使用线上账号或真实渠道密钥。本地数据库独立保存在 `runtime/preview.db`，重启后保留你修改的配置，不会反复重置。

预置内容：

- 4 个模型：精确、前缀、后缀匹配，以及 `^gpt-.*-luna$` 正则分流。
- 3 个渠道：原名转发、固定 `gpt-5.6`、固定 `gpt-6.0`；可以查看 20/120 秒、0 和继承默认值的请求设置。
- 2 把演示密钥：相同渠道分别采用原名优先、5.6 优先。
- 1 个原名优先配置模板，供查看和编辑模板优先级。

渠道默认禁用，仅指向 `127.0.0.1` 的占位地址。这里用于查看、填写和保存界面配置，没有接入真实推理服务。

## 重新启动

用三个独立 PowerShell 终端分别执行：

```powershell
pwsh -File F:/GitProject/axon-hub/output/local-preview/axon-routing/run-gallery.ps1
pwsh -File F:/GitProject/axon-hub/output/local-preview/axon-routing/run-backend.ps1
pwsh -File F:/GitProject/axon-hub/output/local-preview/axon-routing/run-frontend.ps1
```

只看截图时，仅启动第一个即可。实际操作表单先启动后端，看到服务就绪后再启动前端；前端脚本会自动运行 `seed-demo.ps1`。已有依赖沿用仓库当前安装结果，不运行 lint 或 build。

服务已经运行时，可单独补齐演示数据，无需重启：

```powershell
pwsh -File F:/GitProject/axon-hub/output/local-preview/axon-routing/seed-demo.ps1
```

脚本只访问由本目录启动的本地后端。已完成演示初始化时不再写入；如果数据库已由你自行初始化，则跳过自动导入，保留你的账户和数据。
演示导入失败会显示具体错误，但不阻止前端开发服务启动。

## 本次检查

已运行 `seed-demo.ps1` 完成首次导入，并再次运行确认跳过已完成数据。GraphQL 回读确认三个渠道的请求设置与两把密钥的不同优先级均已保存。浏览器已验证四个模型可见、Luna 规则匹配成功、两份密钥配置和模板正常打开。

检查中补齐了演示配置必需的空 `modelMappings` 数组；当前脚本和本地样例均已修正。`seed-demo.ps1`、`run-frontend.ps1` 的 PowerShell 语法检查通过。本轮未运行 lint/build，也未重启服务。

## 停止

手动运行时，在对应终端按 `Ctrl+C`。也可以停止这里记录的本次预览进程：

```powershell
pwsh -File F:/GitProject/axon-hub/output/local-preview/axon-routing/stop.ps1
```

停止脚本会核对 PID、启动时间和程序路径，不按进程名称批量关闭。可加 `-WhatIf` 先查看目标。
所有监听仅绑定 `127.0.0.1`；没有设置系统服务、计划任务或开机启动。
