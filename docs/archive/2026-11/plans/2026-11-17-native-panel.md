> 历史资料。归档日期：2026-11-17。
> 原因：阶段实现已完成，手势和技术方案经历后续调整；正文仅供追溯，不作为当前操作说明。
> 当前替代：[当前状态与文档入口](../../../README.md)。

# Windows 原生配置面板 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 用户在原生窗口修改绑定并保存即时生效，双手手势同样通过绑定驱动，保留现有真人验收行为。

**Architecture:** 识别输出独立于启用绑定；引擎按配置分发单手/双手动作；统一配置服务负责迁移、修订、校验、原子保存及运行切换。原生面板消费稳定回调接口，与引擎通过配置模型衔接，现有预览继续复用。

**Tech Stack:** Go 1.27.2、Win32、现有 ONNX Runtime 1.23.2，无新增运行时依赖。

**Spec:** [原生面板设计](../../../superpowers/specs/2026-11-17-control-panel-design.md)

## Global Constraints

- 用户明确选择 Windows 原生窗口；没有网页服务或 WebView。
- 保留现有灵敏度、默认手势、原生 Windows 输入和性能优化，不初始化 Git 仓库。
- 默认固定十种基础识别手势；绑定启停不得改变分类优先级。
- 暂停、配置切换、退出均释放持有输入；后台不保存摄像头图片。
- 面板关闭隐藏并保留草稿，F7 重开；F9 / 退出按钮停止整个程序。

## Review Focus

1. 旧设置迁移和六项倍率不能重复相乘；覆盖写失败保留旧运行状态。
2. 全部绑定清空/禁用时不缩放、不抢指针、不回落为别的手势动作。
3. 原生滑块/F6 修改后，旧面板草稿不能覆盖新配置。
4. 按键返回保护按数据工作，短暂未知手/丢手不能重新武装点击。
5. 原生窗口消息、后台回调和摄像头处理不能互锁，隐藏预览暂停时跳过推理。

## Task 1: 数据驱动引擎与组合事件

**Files:** `internal/engine/{catalog,engine,controllers,pair_zoom,quick_switch}.go`、新增组合事件/引擎测试。

**Interfaces:** `engine.Binding.ReleaseTo []string`；虚拟姿势 `two_index`、手别 `both`；新增 `pair_zoom`、`confirm_action`、`cancel_action` 动作；`engine.SupportsInput(action string, both bool) bool`；`PointerSettings.LeftSensitivity/RightSensitivity/DragSensitivity float64`（默认 1）。距离缩放仍使用现有 Settings.PairZoom 的全局倍率，但是否启用只由绑定决定。

- [x] 写失败测试：双食指禁用不占用、绑定 Enter 单次且持有期间占用；更换确认姿势；快挥触发任意离散动作；自定义返回姿势保护；倍率乘一次、未知手不输出。
- [x] 实现组合检测只产出匹配/距离数据、引擎分发与动作兼容校验；单手匹配排除 `both`。快挥保留释放拖拽后快切的已有语义，普通快挥键盘动作也可路由。
- [x] 运行相关测试，旧后端测试兼容；默认改动在 Task 2 对接后运行全套控制测试。

## Task 2: 版本化配置与原子应用

**Files:** 新增 `internal/control/{configuration,service}.go` 及测试，修改 `controller.go`、`bindings.go`、`preferences.go`。

**Interfaces:**

```go
type RecognitionSettings struct { VoteWindowMs, LostGraceMs, MiddleConfirmMs, OKConfirmMs, FiveConfirmMs int }
type Configuration struct { Version int; Preferences Preferences; Recognition RecognitionSettings; Gestures []string; Bindings []engine.Binding }
type Snapshot struct { Config Configuration; Revision uint64 }
func DefaultConfiguration(p Preferences) Configuration
func DefaultRecognition() RecognitionSettings
func LoadConfiguration(path string) (Configuration, error)
func SaveConfiguration(path string, cfg Configuration) error
func (cfg Configuration) Clone() Configuration
func (cfg *Configuration) Normalize() error
func GestureCatalog() []gesture.PoseDef // ten canonical + virtual two_index
func ActionCompatible(hand, pose, action string) bool
func NewService(path string, cfg Configuration, inj engine.Injector) (*Service, error)
func (s *Service) Snapshot() Snapshot
func (s *Service) Apply(expected uint64, cfg Configuration) (Snapshot, error)
func (s *Service) UpdatePreferences(p Preferences) (Snapshot, error)
func (s *Service) ToggleHands() (Snapshot, error)
func (s *Service) Controller() *Controller
```

配置 JSON 字段为小写驼峰；版本为 2。初次迁移旧七字段文件前备份为 `<path>.v1.bak`；不得覆盖已有备份。导入上限 1MiB。空绑定明确保持为空；默认绑定为完整已验收预设加 `both/two_index/hold/pair_zoom`。左中指 Tab 的 ReleaseTo 默认为 fist。默认绑定动作倍率为 1，全局倍率由 Settings 提供。

- [x] 失败测试覆盖旧文件迁移保留值、深拷贝、空绑定/错误版本/冲突/NaN拒绝、过期修订、模拟写盘失败、调整偏好保留绑定、重启保留改键、禁用动作仍识别中指。
- [x] 先准备独立的新识别器/引擎并校验，再原子落盘；成功后锁住控制器、释放旧输入、切换快照，保留当前暂停状态。所有写入口经过 Service，落盘期间不持有摄像头处理锁。
- [x] `go test -count=1 ./internal/control ./internal/engine ./internal/gesture` 通过。

## Task 3: 原生面板（可与核心实现按约定接口独立开发）

**Files:** 新增 `internal/panel/{window,editor,settings,files}_windows.go` 及原生测试。

**Interfaces:**

```go
type Options struct {
 Snapshot func() control.Snapshot
 Apply func(uint64, control.Configuration) (control.Snapshot, error)
 Paused func() bool
 SetPaused func(bool)
 TogglePreview func()
 Exit func()
}
func New(Options) (*Window, error)
func (w *Window) PublishStatus(string)
func (w *Window) Show()
func (w *Window) Close()
```

- [x] 绑定列表与编辑表单含启用、名称、手别、手势、触发、等待/重复、动作、动态参数和 ReleaseTo 多选；保存前提交当前表单，冲突保留草稿并显示错误。完整草稿保存带 Revision。
- [x] 原生按钮切换绑定/灵敏度识别/配置管理页面；六项基础倍率、五项识别等待；文件对话框导入/导出、分别恢复默认映射和倍率，导入不直接应用。切换行/页面保留表单草稿。
- [x] 窗口定时消息读取暂停状态和版本更新，避免覆盖脏草稿。关闭隐藏，保留草稿；Close 为程序结束时无条件销毁。
- [x] `MOUSE_PANEL_TEST=1 go test -race -count=1 ./internal/panel` 实际原生消息验证编辑改键、启停、新增删除、导入导出草稿、恢复默认和 stale revision；不发送真实系统输入。

## Task 4: 入口与预览集成

**Files:** `cmd/mouse/main.go`、`internal/preview/window_windows.go`、`internal/control/run.go` 及相关测试。

- [x] CLI 默认原生面板开启，`-panel=false` 兼容无面板运行；统一 Service 保存入口，F6 合并最新配置，F7 显示面板，F8/F9 保留。
- [x] 预览支持 Show/Hide/Visible 和面板模式关闭只隐藏；程序 Close 仍真正销毁。隐藏时不转换 RGB；暂停且预览隐藏时跳过推理，面板状态仍可读。
- [x] OnFrame 每约 0.5 秒更新面板文本状态，渲染与模型处理不共享长锁；关闭整个程序正常释放全部输入/窗口。
- [x] 相关 Run、原生预览/面板生命周期测试通过。

## Task 5: 端到端验收与恢复可测试运行

- [x] `go test -race -count=1 ./...`、`go vet ./...`、构建全部命令和参考图片识别校验通过。
- [x] 独立整体验证配置迁移、原生表单到配置到输入映射、组合动作与返回保护；修复重要问题并复测。
- [x] 正常关闭旧程序，保留旧 EXE/配置备份，启动新原生面板；核对窗口响应、现有七项偏好与日志；核对隐藏面板不产生持续绘制/视频编码开销。
- [x] 更新 README 和验收记录，明确当前可编辑与可验证的功能。

## 执行记录

- 用户已确认使用 Windows 原生面板。实现与检查在当前工作目录继续；无 Git 元数据，跳过 worktree/commit 工具，不创建替代仓库。
- 根据稳定接口将原生窗口与核心配置/动作实现分开，集成时统一校验；具体实现分工属于工作安排，不再次要求用户选择流程。

- 完成记录：配置、引擎、原生面板、入口和预览均已接通；全量竞态测试、原生控件交互、vet、GUI 构建和 Windows 实际鼠标输入验证通过。独立审查的重要问题均先复现再修复。完整证据及截图工具限制见[验收记录](../../../native-panel-validation-2026-11-17.md)。
- Ruling：使用原生控件，不保留网页接口；执行采用按稳定接口分工的独立实现与审查。无 Git 元数据，跳过依赖 Git 的 worktree/commit 脚本，未初始化仓库。
- Ruling：导入未知动作参数明确拒绝，避免参数拼写错误静默变为默认快捷键。确认/取消属于单手当前模式动作，双手选项不提供不可达的确认/取消绑定。
