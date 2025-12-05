# 1. 后端测试目标与金字塔

这个项目后端最大的坑在：

- **对局状态机正确性**（三圈规则、allowedActions、拖动=敲波波、第三圈强制比牌）
- **结算一致性**（输赢+抽成+代理分润+芒果连忙+钱包守恒）
- **匹配风控**（距离/IP 硬拦截、同桌惩罚）
- **WS 断线重连/幂等**
- **复盘日志与真实对局一致**

建议测试金字塔：

1. **单元测试 55%**：纯函数/算法/状态机片段
2. **组件/服务测试 25%**：MatchService、WalletService、AgentService、RakeService
3. **集成测试 15%**：PG+Redis 起容器跑真实事务
4. **E2E/压力 5%**：WS 多房间并发、断线重连、长连接稳定

------

# 2. 测试环境与工具

## 2.1 Go 测试基础

- `testing` 标准库
- 断言：`stretchr/testify`（match/require）
- mock：`gomock` 或 `testify/mock`
- 覆盖率：`go test -coverprofile`

## 2.2 集成测试容器

- `testcontainers-go` 或 docker-compose
  - PostgreSQL、Redis、（可选 Nginx/WS）
- 每个 test suite 启一个隔离的 DB schema

## 2.3 E2E & 并发模拟

- WS 客户端：用 Go 原生/`gorilla/websocket` 写测试机器人
- 并发/压力：`k6`（HTTP+WS），或 Go 自己起 goroutine 压测

------

# 3. 分层测试流程（从开发到发版）

## 3.1 开发期（每个 PR 必跑）

### A. 单元测试（Unit）

覆盖模块：

1. **抽成规则 RakeRule.Apply()**
   - 固定比例 / 固定值 / 阶梯
   - 上下限
2. **代理分润 AgentShare.Calc()**
   - L1 4:6
   - 多级比例递减
   - 平台剩余归集
3. **芒果连忙 MangoStreak.Update()**
   - 修忙产生
   - 皱芒不产且断连
   - 比牌断连
   - 3忙保持
4. **距离/IP 判定**
   - Haversine 距离计算的边界
   - 同网段/IP 视作距离=0
5. **对局状态机最小片段**
   - 第一圈下家 pass/call(2×皮)合法性
   - raise≥5×屁
   - raise≥lastRaise
   - 第二圈拖动=knock_bobo
   - 第三圈禁止发照直接比牌

> 单元测试要求：**任何一个“规则点”都要有正反例**。

### B. 服务测试（Service）

对每个 service 用 mock repo：

- MatchService.join/cancel/status
- WalletService.freeze/unfreeze/settle
- GameService.handleAction/handleTimeout
- ReplayService.buildTimeline
- AdminService.updateRakeRule/updateScene

重点看：

- 输入校验
- 输出 DTO
- 错误码
- 幂等分支

------

## 3.2 集成期（每天/合并到 develop 必跑）

### 集成测试（Integration）

起真实 PG + Redis：

**必测场景**

1. **一局结算事务守恒**
   - 多人下注→赢家→抽成→多级代理
   - 断言：
     - 每个钱包余额变化与 billing_logs 对齐
     - 平台抽成收入=赢家抽成池-代理分润
     - DB 中 matches/rake_json 可复算
2. **匹配 hard condition**
   - 近距离两个用户 join 同场次
   - matcher 不能把他们配成一桌
3. **rejoin 幂等**
   - 同一个 match settle 触发两次
   - ended_at 已写入则第二次无副作用
4. **复盘日志一致**
   - 对局 log 写到 round_logs
   - replay API 读取并输出 timeline
   - timeline 步骤数、顺序、round_no 一致

------

## 3.3 发版前（release 分支）

### E2E/回归（HTTP + WS）

写“测试机器人”跑全链路：

1. 3~4 个机器人登录 → join matchmaking
2. 成桌后通过 WS 依次发送动作
3. 覆盖三圈：
   - 第一圈 pass/call/raise
   - 第二圈拖动=knock
   - 第三圈等待 server 自动摊牌
4. 等待结算 → 校验钱包/抽成/分润
5. 调 replay API → 校验 timeline 可回放

------

# 4. AI 自动测试（后端怎么用 AI 才真省人）

AI 在后端主要用 4 件事：
 **生成用例、生成机器人脚本、生成属性测试、自动对账定位失败原因。**

## 4.1 AI 生成测试用例清单（从 PRD/规则文本）

把你那份规则+PRD喂给 AI，让它产出：

- 状态机用例表（轮次×动作×合法性×结果）
- 芒果/修忙/皱芒的状态转移表
- 抽成/代理分润的对账用例
- 匹配硬约束用例

以后规则改动，把变化段贴给 AI，它会**增量更新用例**。

## 4.2 AI 生成“对局机器人脚本”

你只需要给 AI 说明：

- WS 消息格式
- allowedActions 约束
- 场次底皮/屁/阈值
   AI 就能生成一份 Go 机器人脚本，用于 E2E / 压测。

核心逻辑（机器人）：

```
for state := range stateCh {
  act := chooseAllowed(state.AllowedActions)
  sendAction(act)
}
```

> 你后续新增玩法/规则，也让 AI 直接改 chooseAllowed 策略。

## 4.3 AI + 属性测试（Property-based Testing）

适合“结算守恒/抽成分润”这种强约束逻辑。

思路：

- 随机生成 N 局输赢/下注/抽成规则/代理链
- 断言“不变量”永远成立
- 失败时 AI 输出最小反例

不变量例子：

1. **余额守恒（总积分变化只来自充值/系统抽成）**
2. **代理分润 <= 抽成池**
3. **streak 只在 {0,1,2,3}**
4. **第三圈无 allowedActions 中的发照/说钱类动作**

Go 可用 `testing/quick` 或自己写 random generator，AI 帮你生成生成器与断言。

## 4.4 AI 自动对账/定位失败

当集成/E2E 失败时，把以下产物交给 AI：

- 失败 case 的 actions_json
- 结算前后 billing_logs
- matches.rake_json
- wallets 快照
   AI 输出：
- 哪一步不一致
- 可能 bug 点（例如 lastRaise 更新、streak 断连逻辑、L2 分润比例使用错误）

这样排查时间会大幅缩短。

------

# 5. 关键测试用例（后端必测清单）

## 5.1 状态机（WS）

1. 第一圈下家 **pass 合法**
2. 第一圈下家 **call 必须=2×皮**
3. 首次 raise **>=5×屁**
4. 再 raise **>=lastRaise**，否则转为 knock
5. 第二圈拖动→服务端记录 knock_bobo 并广播
6. 第三圈任何“发照/说钱/raise”动作 → reject
7. 非 turnSeat 玩家 action → reject
8. 超时 → 自动 pass/fold 与 allowedActions 一致
9. 断线重连 rejoin → 下发全量 state 与桌面一致

## 5.2 芒果/修忙/皱芒

1. 修忙连续三局：streak 1→2→3
2. streak=3 后无人说钱：保持 3
3. 任一局发生比牌：streak 归 0
4. 第二圈说钱无人买＝皱芒
   - 不产生芒果
   - streak 断
   - 翻查结算类型正确

## 5.3 抽成/代理

1. 固定比例抽成
2. 固定值抽成
3. 阶梯抽成
4. L1 4:6 分润正确
5. 多级代理分摊正确
6. 平台剩余归集正确
7. 绑定邀请码只生效一次

## 5.4 匹配

1. 满足条件→能成桌
2. 近距离 pair → 永不成同桌
3. 同 IP/网段 pair → 永不成同桌
4. 取消匹配后队列移除
5. 匹配锁 TTL 到期可再入队

------

# 6. CI 流水线（建议）

**PR 阶段：**

1. `go test ./...`（单测+服务测）
2. `golangci-lint run`
3. 覆盖率阈值（比如 70%）

**develop 日构建：**

1. 起 testcontainers（PG+Redis）
2. 跑 integration suite
3. AI 汇总报告（可选）

**release 发版：**

1. 全量 unit/service/integration
2. E2E 机器人跑 50~100 局
3. k6 压测 5~10 分钟
4. 汇总对账报告