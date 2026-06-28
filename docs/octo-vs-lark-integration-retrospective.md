# Octo vs Lark 集成实现复盘与 GAP 分析

> 背景:本次将 fork 同步到 upstream（v0.3.27..v0.3.31，54 commits）时,最棘手的冲突来自 upstream 的 **MUL-3620**——它把原本 Feishu 专用的 `lark.Hub` 重构成了通用的 `channel.Channel` 引擎,并以此接入了 Slack。而 fork 的 **Octo** 集成是基于**旧版 Lark 结构**实现的,因此与新架构产生了结构性错位。本文系统复盘 Lark 的实现方式,并据此分析 Octo 实现存在的 GAP。

---

## 一、时间线:理解 GAP 的根因

| 迁移号 | 内容 | 来源 | 含义 |
| --- | --- | --- | --- |
| `109` | `lark_integration` | upstream | Lark 专用表 `lark_*`,Feishu-only 的 Hub/Dispatcher |
| `120` | `octo_integration` | **fork** | Octo 自己的一套**平行表** `octo_*`,镜像了**当时**的 Lark 结构 |
| `124` | `channel_generalization` | upstream (MUL-3515/3506) | 将 `lark_*` 泛化为共享的 `channel_*` 表 + `channel.Channel` 引擎 |
| (MUL-3620) | 引擎落地 | upstream | Feishu 成为 `channel.Channel` 的实现;Slack 作为第二个消费者接入 |

**根因一句话**:Octo 在「共享 channel 框架(124)还不存在」的时候诞生(120),它合理地参照了**当时唯一的范例**——旧版 Lark。但 upstream 随后把 Lark 抽象成了共享引擎,Octo 没有跟着迁移,于是从「与 Lark 同构」变成了「与全平台共享引擎平行的一套独立栈」。

这不是「当时做错了」,而是「范式在 Octo 之后发生了迁移,Octo 产生了架构债」。

---

## 二、Lark 集成的实现方式(post-MUL-3620 的标准范式)

Lark 现在是「如何在本仓库实现一个 IM 集成」的**标准答案**。它的核心是**契约与实现分离**:平台特有的东西全部塞进适配器,业务逻辑全部走共享引擎。

### 2.1 四件套契约(`server/internal/integrations/channel/`)

`channel/doc.go` 定义了每个集成都要实现的契约:

1. **`Channel` 接口**(`channel.go:22-74`)——每个平台只实现 5 个方法:
   `Type()` / `Connect(ctx)` / `Disconnect(ctx)` / `Send(msg)` / `Capabilities()`。
   引擎永远不碰平台 SDK 或裸协议。
2. **`InboundMessage` / `OutboundMessage`**(`message.go`)——归一化信封。每个平台的入站报文由适配器翻译成**一个** `InboundMessage`,核心只路由/去重/落库这一种结构。平台特有字段塞进 `Raw`(JSONB),**只有产生它的适配器**才读。
3. **`Capability` 位掩码**(`capability.go`)——平台**声明**自己支持什么(富卡片、线程、附件…)。引擎不做降级,降级由调用方读位掩码自行决定。
4. **`Registry`**(`registry.go`)——`Type → Factory` 映射。新增平台 = 「注册一个 factory」,而非「改核心」。

### 2.2 共享引擎(`channel/engine/`)

| 组件 | 文件 | 职责 |
| --- | --- | --- |
| `Supervisor` | `engine/supervisor.go` | 多副本 DB 租约 + 每安装一个 goroutine;退避重连;优雅停机 |
| `Router` | `engine/router.go` | **共享入站管线**:解析安装 → 两段式去重 → 身份解析 → 会话落库 → `/issue` 命令 → 触发 run |
| `ResolverSet` | `engine/resolvers.go` | 7 个可插拔接口(Installation/Identity/Dedup/Session/Audit/Replier/Typing)+ 哨兵错误 |
| `ChatSession` | `engine/session.go` | 跨平台共享的会话服务:EnsureSession + AppendMessage(事务内打去重标记) |
| `IssueCommand` | `engine/issue_command.go` | `/issue` 命令解析(含「裸 /issue 回退到上一条消息」) |
| `Batcher` | `engine/batcher.go` | run 触发去抖(默认 3s 窗口),把「转发一堆消息 + 一句话」合并成一次 run |

### 2.3 Lark 适配器层(`server/internal/integrations/lark/`)

- **`feishu_channel.go`**——`channel.Channel` 实现。`Connect()` 跑 WS 长连,把每条 Lark 报文翻成 `channel.InboundMessage`(`channelMessageFromLark`);`Send()` 走 HTTP;`Capabilities()` 声明 7 项能力。
- **`feishu_resolvers.go`**——Lark 的 `ResolverSet`,把 7 个接口对接到 `channel_*` 表。
- **传输层**——`ws_connector.go`(长连 + 看门狗 ctx 取消)、`ws_frame_decoder.go`、`ws_chunk_assembler.go`、`ws_endpoint.go`。
- **入站增强**——`inbound_enricher.go`(拉取被引用消息/合并转发上下文,2s 超时,best-effort)、`content_flatten.go`、`markdown_detect.go`。
- **出站**——`outbound.go`(任务生命周期**卡片 patch**:Thinking → Working → Final/Error,带节流)、`typing_indicator.go`(入站加 typing 反应,任务结束移除)、`outcome_replier.go`(未绑定发绑定卡、agent 离线/归档通知)。
- **身份绑定**——`binding_token.go`(15min 一次性 token,只存 hash)、`registration_service.go`(RFC 8628 设备流,自动绑定安装者)、`installation.go`(app_secret 静态加密)。
- **审计**——`audit.go`(`channel_inbound_audit`,**只记路由/去重元数据,不记正文**)。

### 2.4 接线(`router.go:221-360`)

```go
channelRegistry := channel.NewRegistry()
channelRouter   := engine.NewRouter(h.IssueService, h.TaskService, queries, ...)
channelRouter.EnableRunBatching(engine.DefaultChatRunBatchWindow)
h.ChannelRouter = channelRouter
h.ChannelSupervisor = engine.NewSupervisor(..., channelRegistry, channelRouter.Handle, ...)

// 每个平台只做两件事:注册 factory + 注册 resolver set
lark.RegisterFeishu(channelRegistry, ...)
channelRouter.Register(channel.TypeFeishu, lark.NewFeishuResolverSet(...))
slack.RegisterSlack(channelRegistry, ...)
channelRouter.Register(slack.TypeSlack, slack.NewSlackResolverSet(...))
```

**关键性质**:新增一个平台,引擎/Router/ChatSession **零改动**。Slack 正是这样接入的——它就是「实现 Channel + 实现 ResolverSet + 注册」三步。

---

## 三、Octo 集成的实现方式

Octo 的 `doc.go` 自述「follows the same structural boundaries as the Lark integration」——但这里的 Lark 指的是**迁移 120 时点的旧 Lark**。

### 3.1 分层(transport / business)

- **transport 子包**(`octo/transport/`)——WuKongIM 二进制协议(`socket.go` DH 密钥交换 + AES-GCM 解密 + 心跳 + 重连 + RECVACK;`codec.go` UTF-16 偏移帧编解码;`crypto.go`;`packet.go`)+ Octo REST(`http_client.go`)。这一层做得**扎实且独立**,本身没有问题。
- **business 层**(`octo/`)——`Hub` / `Dispatcher` / `Connector` / `Patcher` / `OutcomeReplier` / `BindingTokenService` / `InstallationService` / `ChatSessionService` / `Audit`。

### 3.2 自有的一套平行栈(核心事实)

直接验证结论:

```
grep "integrations/channel" server/internal/integrations/octo/  → 无任何引用
```

Octo **完全不 import** 共享的 `channel` / `engine` 包。它有自己的:

| Octo 自有组件 | 对应的共享引擎组件 | 关系 |
| --- | --- | --- |
| `octo/hub.go`(租约 + supervise + sweep) | `engine/supervisor.go` | **重复实现** |
| `octo/dispatcher.go`(入站管线) | `engine/router.go` | **重复实现** |
| `octo/chat_service.go`(会话落库) | `engine/session.go` | **重复实现** |
| `octo/connector.go` | `channel.Channel.Connect` | **重复实现** |
| 两段式去重(Claim/Mark/Release) | `engine` 同款两段式 | **逻辑几乎逐行重复** |
| `octo_*` 表(120) | `channel_*` 表(124) | **平行表** |

### 3.3 复用的部分(做得对的地方)

Octo 并非全盘重造,它正确复用了**服务层**:
- `service.TaskService.EnqueueChatTask()` —— agent 任务入队
- `chat_session` / `chat_message` 表 —— 会话内容(这是早于 channel 框架就泛化好的)
- 事件总线 `protocol.EventChatDone` / `EventTaskFailed` —— 出站中继
- agent/daemon/task 执行引擎

所以 GAP **不在**「业务深度」,而在「入站/连接/去重/会话**编排层**没有走共享引擎」。

---

## 四、系统性 GAP 分析

### GAP 1:架构错位——平行栈 vs 共享引擎(根本性)

**现状**:upstream 现在是「一个 Supervisor + 一个 Router + N 个 ResolverSet」。Octo 是第三套独立的 Hub+Dispatcher+Connector,与 `ChannelSupervisor`/`ChannelRouter` 并排运行(`main.go` 里 `h.ChannelSupervisor.Run` 与 `h.OctoHub.Run` 各跑各的)。

**影响**:
- 两套租约逻辑、两套去重逻辑、两套会话落库逻辑需要**各自维护、各自测试、各自修 bug**。本次同步的 `task.go` 冲突就是活例:upstream 给共享路径加了 `broadcastIssueUpdated`,Octo 路径若有同类问题需要单独再修一遍。
- upstream 对引擎的任何改进(性能、正确性、新能力)Octo **自动享受不到**。

**严重度**:🔴 高(是其它所有 GAP 的根源)

### GAP 2:缺失 `/issue` 命令支持(功能缺失)

```
grep "/issue" server/internal/integrations/octo/dispatcher.go  → 无
```

Lark/Slack 用户可以在 IM 里发 `/issue <标题>` 直接建 issue(`engine/issue_command.go`),**Octo 用户不能**。Octo 只实现了 `/new`(`fresh_command.go`)。这是共享引擎免费提供、而平行栈需要自己补的能力差。

**严重度**:🟠 中(产品功能不对齐)

### GAP 3:缺失 run 去抖批处理(行为差异)

Lark/Slack 经 `engine/batcher.go` 做 3s 去抖:用户连发/转发多条时合并成**一次** agent run。Octo `dispatcher.go` **每条消息立即入队一次 run**。

**影响**:用户在 Octo 里粘贴多段内容会触发多次 agent 执行,既费算力又导致回复错乱。

**严重度**:🟠 中

### GAP 4:出站能力薄弱——无 typing、无流式 patch(体验差异)

| 能力 | Lark | Octo |
| --- | --- | --- |
| 输入中 typing 提示 | ✅ `typing_indicator.go` | ❌ 无 |
| 任务进行中流式更新卡片 | ✅ `outbound.go` patch(Thinking→Working→Final) | ❌ 仅在 `chat:done`/`task:failed` 时发一条终态文本 |

Octo 用户从发消息到收到回复之间**完全没有反馈**,长任务体验明显更差。(部分受 WuKongIM 协议能力限制,但 typing/中间态至少可做。)

**严重度**:🟡 中低(体验,非正确性)

### GAP 5:能力声明缺位(扩展性)

Octo 没有 `Capabilities()` 位掩码声明(因为它不实现 `channel.Channel`)。未来若引擎按能力做统一降级/渲染策略,Octo 仍游离在外,需要单独适配。

**严重度**:🟡 低

### GAP 6:数据模型分叉(维护成本)

`octo_*` 表(installation/user_binding/chat_session_binding/inbound_dedup/inbound_audit/outbound_message/binding_token)与 `channel_*` 表是**结构几乎相同的两套**。任何对「安装/绑定/去重/审计」模型的演进都要写两遍迁移、两遍查询。

**严重度**:🟠 中

### 做得好的地方(不应否定)

- ✅ transport 子包(WuKongIM 协议)实现扎实,加密/编解码/重连/防 replay 都有测试。
- ✅ 两段式去重、租约 fence、token 静态加密、绑定 token 只存 hash、审计不记正文——这些**正确的安全/正确性模式都照搬到位了**。
- ✅ 正确复用了 `TaskService`、`chat_session`、事件总线,没有重造执行引擎。
- ✅ 前端按仓库规范用 zod + `parseWithFallback` 校验响应。

换句话说:**Octo 把「当时的 Lark」抄得很好,问题纯粹是 Lark 后来升级了而 Octo 没跟。**

---

## 五、收敛建议(按性价比排序)

> 原则:遵循 CLAUDE.md「若旧路径正被替换且产品未上线,优先删除旧路径而非双轨并存」「内部非边界代码不要加兼容层」。Octo 与共享引擎的关系正是「应当收敛为单轨」的典型。

### 方案 A(推荐,治本):把 Octo 重构为 `channel.Channel` 实现

让 Octo 成为继 Feishu、Slack 之后的**第三个共享引擎消费者**:

1. **`octoChannel` 实现 `channel.Channel`**——`Connect()` 跑 WuKongIM 长连(复用现有 transport 子包,这部分不动),把入站报文翻成 `channel.InboundMessage`;`Send()` 走现有 REST;`Capabilities()` 声明文本能力。
2. **`NewOctoResolverSet()` 实现 `ResolverSet`**——把 Installation/Identity/Dedup/Session/Audit/Replier 接到现有数据(可保留 `octo_*` 表,resolver 内部适配;或迁移到 `channel_*`,见步骤 4)。
3. **接线收敛**——`octo.RegisterOcto(channelRegistry, ...)` + `channelRouter.Register(octo.TypeOcto, ...)`,**删除** `OctoHub` 独立 Run 路径。
4. **数据收敛(可分期)**——将 `octo_*` 迁移并入 `channel_*`(加 `channel_type='octo'` 判别列),最终删除 `octo_*` 表与平行查询。

**收益**:立刻白嫖 `/issue`、run 去抖、未来所有引擎改进;删掉约一半 Octo 代码(hub/dispatcher/chat_service/connector 编排逻辑);本次这类同步冲突今后不再发生。

**成本**:一次性中等重构 + 数据迁移;需补「WuKongIM 长连如何套进 `Supervisor` 的 Connect/Disconnect 生命周期」的适配(Feishu 的 `feishu_channel.go` 是现成范本)。

### 方案 B(折中,治标):保留平行栈,补齐功能差

若暂时不做大重构,至少把 GAP 2/3/4 的功能补到 Octo:复用 `engine.ParseIssueCommand`、引入去抖、加 typing。

**缺点**:不解决根因,反而**加深**重复(去抖/命令解析又各写一份),与 CLAUDE.md「不要平行抽象」相悖。**不推荐**作为终态,仅作为重构前的过渡。

### 方案 C(不动):接受现状

仅当 Octo 即将下线、或 WuKongIM 与共享引擎的连接模型确实无法调和时才选。目前证据不支持「无法调和」——transport 是可复用的纯 I/O 层,Feishu 的 WS 长连已证明长连模型能套进 `Supervisor`。

---

## 六、给后续集成的经验教训

1. **新集成落地前,先确认当前的「标准范式」是什么。** Octo 的问题不是实现质量,而是「参照了一个即将被替换的范例」。本仓库现在的范式明确是 `channel.Channel` + `engine`,新平台应直接对接,而非另起炉灶。
2. **平行栈的代价是复利的。** 每次 upstream 改进共享引擎,平行栈都要么手动跟进、要么落后——同步成本随时间线性累积,本次冲突已是第一笔利息。
3. **transport 与 orchestration 要分开评估。** Octo 的 transport 层值得保留,真正该收敛的是上层编排。重构时不要把「写得好的协议层」和「该并轨的编排层」一起推倒。
4. **「跟随 X 的结构」这类注释要带版本锚点。** `doc.go` 写「follows the Lark integration」却没说是哪个时点的 Lark,后来读者很难意识到范式已迁移。
