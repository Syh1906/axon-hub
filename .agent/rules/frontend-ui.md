---
alwaysApply: false
globs: "frontend/**/*.tsx"
---

# 前端界面与交互约定

## 同类后台功能的设计基准

- 后续同类后台功能沿用已确认的增量界面形式：在现有页面、表单、弹窗中增加必要的字段、区块或入口，保留原有信息结构、功能位置和操作路径。除非用户明确要求重设计，不另做整页布局或把不同职责集中到一个新页面。
- 实现参考为模型创建／编辑、模型关联、密钥渠道优先级和渠道请求设置：
  `frontend/src/features/models/components/models-action-dialog.tsx`、
  `frontend/src/features/models/components/models-association-dialog.tsx`、
  `frontend/src/features/apikeys/components/channel-priority-editor.tsx`、
  `frontend/src/features/channels/components/channels-request-settings-dialog.tsx`。
  复用其组织方式和现有组件，不机械复制字段数量或布局；小表单不强行改成双栏。
- 沿用项目的主题颜色、字体、间距、边框、圆角、图标和按钮层级，使用现有语义样式与 shadcn/ui 组件，不为新功能引入另一套视觉系统。
- 保持现有职责分区：例如模型弹窗左侧为基本配置、右侧为能力与定价；新导入或选择入口放在所影响区块附近。新增复杂列表优先沿用所属页面已有的独立弹窗或侧面板形式，不持续扩宽主表单。
- 长表单保留可见的标题与底部操作区，内容区按原有方式滚动。普通创建／保存作为主操作；新增关联、导入等额外流程由用户主动选择，不在打开表单时自动进入下一步；已确认的字段自动回填按业务规则保留。
- 需要设计预览时，以当前真实界面截图作局部扩展，并明确标注新增内容；不得把另绘整页或重排后的页面作为默认方案。

## 弹窗与候选列表

- 在 `Dialog` 内使用 `AutoComplete` 或 `AutoCompleteSelect` 时，必须传入指向弹窗容器的 `portalContainer`，保证滚动与层级正确。
- 打开长表单时，不自动展开候选列表或滚动到中间字段；保留可见且合理的初始焦点。候选列表支持主动点击、输入与键盘选择，切换字段或按 Escape、Tab 时正确收起。
- 弹窗中的菜单若没有独立阻止外部点击的需求，沿用外层弹窗的限制；验收关闭、取消、保存成功及连续重开后页面仍可操作，避免残留遮罩、焦点或鼠标禁用状态。
