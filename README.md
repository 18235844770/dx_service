# 后端技术开发文档（Go + WebSocket）V1.0

项目：打旋牌线上对局平台
 推荐语言：**Go 1.22+**
 数据库：PostgreSQL 15+
 缓存/队列：Redis 7+
 实时通信：WebSocket（服务端权威状态机）
 部署：Docker / k8s（可单机起步）

------

## 0. 设计原则（必须遵守）

1. **服务端权威**：所有游戏随机、下注合法性、结算、抽成、代理分润都只在服务端完成。
2. **房间状态机单线程化**：每个牌桌一个 goroutine 驱动状态机，所有动作串行处理，避免竞态。
3. **动作可审计可回放**：每个动作写入 `round_logs`，复盘只读日志。
4. **账务原子性**：每局结算涉及多用户钱包变化，必须事务+幂等。
5. **合规（不可用于赌博）**：提供管理端风控/封禁开关、审计日志。

------

## 1. 工程与模块划分

### 1.1 单体起步结构（可平滑拆服务）

```
cmd/server/main.go
internal/
  api/                 # HTTP路由层
  ws/                  # WebSocket网关 & 房间注册
  domain/
    user/
    wallet/
    match/
    game/
    agent/
    admin/
  service/             # 业务服务/用例
  repo/                # 数据库访问（sqlc/gorm任选）
  model/               # Entity/DTO/VO
  middleware/          # JWT、日志、限流
  config/
pkg/
  logger/
  errors/
```

### 1.2 关键依赖

- HTTP：Gin 或 Fiber（二选一）
- WS：原生 `gorilla/websocket` 或 `nhooyr/websocket`
- DB：sqlc（推荐）或 GORM
- Redis：go-redis v9
- 配置：viper
- 日志：zap
- 任务（可选）：asynq

------

## 2. 数据模型（PostgreSQL）

### 2.1 用户与代理

**users**

- id BIGSERIAL PK
- phone TEXT UNIQUE NOT NULL
- nickname TEXT
- avatar TEXT
- location_city TEXT
- gps_lat DOUBLE PRECISION
- gps_lng DOUBLE PRECISION
- invite_code TEXT UNIQUE
- bind_agent_id BIGINT NULL
- agent_path TEXT NULL   -- 形如 "A>B>C"
- status TEXT NOT NULL DEFAULT 'normal'  -- normal/banned
- created_at TIMESTAMPTZ
- updated_at TIMESTAMPTZ

**agents**

- id BIGINT PK (== users.id)
- level INT DEFAULT 1
- total_invited INT DEFAULT 0
- total_profit BIGINT DEFAULT 0
- created_at TIMESTAMPTZ

**agent_profit_logs**

- id BIGSERIAL PK
- agent_id BIGINT
- from_user_id BIGINT
- match_id BIGINT
- level INT
- rake_amount BIGINT
- profit_amount BIGINT
- created_at TIMESTAMPTZ

> `profit_amount`、`rake_amount` 单位都是“积分整数”。

------

### 2.2 钱包与账务

**wallets**

- user_id BIGINT PK
- balance_total BIGINT
- balance_available BIGINT
- balance_frozen BIGINT
- total_recharge BIGINT
- total_win BIGINT
- total_consume BIGINT
- total_rake BIGINT
- updated_at TIMESTAMPTZ

**recharge_orders**

- id BIGSERIAL PK
- user_id BIGINT
- amount_cny INT
- points BIGINT
- status TEXT  -- pending/success/failed/refunded
- channel TEXT
- created_at, paid_at TIMESTAMPTZ
- out_trade_no TEXT UNIQUE

**billing_logs**

- id BIGSERIAL PK
- user_id BIGINT
- type TEXT  -- freeze/unfreeze/win/lose/rake/agent_share/recharge/adjust
- delta BIGINT
- balance_after BIGINT
- match_id BIGINT NULL
- meta_json JSONB
- created_at TIMESTAMPTZ

------

### 2.3 对局与复盘

**scenes**

- id BIGSERIAL PK
- name TEXT
- seat_count INT
- min_in BIGINT
- max_in BIGINT
- base_pi BIGINT         -- 皮
- min_unit_pi BIGINT     -- 屁
- mango_enabled BOOL
- bobo_enabled BOOL
- distance_threshold_m INT
- rake_rule_id BIGINT
- created_at TIMESTAMPTZ

> 调试/演示阶段可在 `config.yaml` 中设置 `features.skipLocationValidation: true`，跳过场景定位/距离风控，后续需要时再关掉该开关即可恢复校验。

**rake_rules**

- id BIGSERIAL PK
- type TEXT   -- ratio/fixed/ladder
- config_json JSONB
- created_at TIMESTAMPTZ

**agent_rules**

- id BIGSERIAL PK
- max_level INT
- level_ratios_json JSONB   -- { "L1":0.4,"L2":0.1... }
- base_platform_ratio FLOAT -- 默认0.6

**tables**

- id BIGSERIAL PK
- scene_id BIGINT
- status TEXT  -- waiting/playing/ended
- seat_count INT
- mango_streak INT DEFAULT 0
- players_json JSONB  -- seat->userId->alias 映射
- created_at TIMESTAMPTZ

**matches**

- id BIGSERIAL PK
- table_id BIGINT
- scene_id BIGINT
- result_json JSONB
- rake_json JSONB
- created_at, ended_at TIMESTAMPTZ

**match_round_logs**

- id BIGSERIAL PK
- match_id BIGINT
- round_no INT
- actions_json JSONB
- cards_json JSONB
- created_at TIMESTAMPTZ

------

## 3. Redis 设计

| key                      | 类型       | 含义                          |
| ------------------------ | ---------- | ----------------------------- |
| `queue:{sceneId}`        | ZSET       | 匹配队列，score=进入时间      |
| `queue:lock:{userId}`    | STRING+TTL | 匹配/开桌锁                   |
| `table:online:{tableId}` | HASH       | WS在线 userId->connId         |
| `table:state:{tableId}`  | STRING     | 房间最新state快照（便于重连） |
| `risk:distance:{userId}` | HASH+TTL   | 最近一次定位                  |

------

## 4. HTTP API（REST）

### 4.1 认证

- JWT access token（2h）
- Header：`Authorization: Bearer <token>`
- middleware：校验 token + status != banned

### 4.2 用户/登录

- `POST /dxService/v1/auth/sms/send`
- `POST /dxService/v1/auth/sms/login`
- `GET  /dxService/v1/user/profile`
- `PUT  /dxService/v1/user/profile`

登录时若 `inviteCode` 有值且用户未绑定 → 绑定上级代理并写 `agent_path`。

### 4.3 大厅/场次

- `GET /dxService/v1/scenes`
- `GET /dxService/v1/wallet`

### 4.4 匹配

- `POST /dxService/v1/match/join`
- `POST /dxService/v1/match/cancel`
- `GET  /dxService/v1/match/status`

### 4.5 记录/复盘

- `GET /dxService/v1/match/list`
- `GET /dxService/v1/match/{id}/replay`

### 4.6 代理

- `GET /dxService/v1/agent/dashboard`
- `GET /dxService/v1/agent/invitees`
- `GET /dxService/v1/agent/profits`

### 4.7 管理端（简版）

> 登录入口：`POST /admin/auth/login`（账号/密码来源于 `config.yaml` -> `admin`，部署后请立即修改默认密码）

- `POST/PUT/GET /admin/scenes`
- `POST/PUT/GET /admin/rake_rules`
- `POST/PUT/GET /admin/agent_rules`
- `GET /admin/users`
- `GET /admin/users/{id}`
- `PUT /admin/users/{id}/ban`
- `PUT /admin/users/{id}/wallet`
- `GET /admin/matches`

------

## 5. WebSocket 协议（对局）

### 5.1 连接

```
wss://host/ws/table/{tableId}?token=...
```

### 5.2 消息结构

```
// client -> server
{ "type":"action", "seq":123, "data":{...} }

// server -> client
{ "type":"state", "seq":124, "data":{...} }
{ "type":"error", "seq":125, "data":{"code":4001,"message":"..."} }
```

### 5.3 动作枚举

- pass（过牌）
- call（跟注）
- raise { amount }
- knock_bobo（敲波波）
- fold（丢）
- ready
- rejoin
- ping

### 5.4 状态下发（全量）

```
{
  "round": 1,
  "turnSeat": 2,
  "lastRaise": 50,
  "mangoStreak": 2,
  "countdown": 12,
  "allowedActions": ["pass","call","raise","fold"],
  "seats": [
    {"seat":1,"alias":"玩家A","chips":900,"status":"alive"},
    {"seat":2,"alias":"玩家B","chips":1100,"status":"alive"}
  ],
  "myCards": ["**","**","**"],
  "logs": [...]
}
```

------

## 6. 匹配服务实现

### 6.1 Join 流程

1. 校验场次 `sceneId` 存在
2. 校验入场积分在 `[min_in, max_in]`
3. 获取/写入 gps + ip（ip 可从 header 拿）
4. 对用户加 `queue:lock:{userId}`（TTL=10s，防重复入队）
5. ZADD `queue:{sceneId}` score=now item=userId
6. 返回 queueId（可直接用 userId）

### 6.2 匹配循环（后台协程）

每个场次一个 matcher goroutine：

```
for {
  ids := ZRANGE queue:{sceneId} 0 K
  tryCompose(ids)
  sleep(500ms)
}
```

### 6.3 tryCompose 算法

- 从队列头 K 人中找 N 人组合
- **硬条件**：
  1. 任意两人距离 >= threshold
  2. IP 不同 / 不同网段
  3. 余额满足 min_in
- 组合成功 →
  - 给这 N 人上 `queue:lock`（SETNX）
  - ZREM 出队
  - 创建 table + match init
  - 通知这 N 人 `matched(tableId)`（通过 HTTP轮询即可）

距离计算：Haversine（后端模块 `utils/geo.go`）。

------

## 7. 牌桌房间模型（Game Service）

### 7.1 核心对象

```
type TableRoom struct {
  tableID   int64
  scene     Scene
  seats     []Seat
  round     int
  turnSeat  int
  lastRaise int64
  mangoStreak int
  deck      []Card
  actionsCh chan PlayerAction
  quitCh    chan struct{}
}
```

### 7.2 单房间单 goroutine

- `Start()` 开一个 goroutine：

```
go func(){
  for {
    select {
     case act := <-actionsCh:
        room.handleAction(act)
     case <-timer.C:
        room.handleTimeout()
     case <-quitCh:
        return
    }
  }
}()
```

**保证所有状态修改都在一个线程中完成。**

### 7.3 断线重连

- WS 断开只标记 seat offline，不退出房间
- 重连 `rejoin` → 回全量 state
- 若倒计时超时且还未回到 turnSeat → 自动默认动作（pass/fold）

------

## 8. 对局状态机（按你的玩法）

### 8.1 轮次定义

- **Round0**：开局扣皮、发两张牌
- **Round1（第一圈）**：两张后发话
- **Round2（第二圈发照）**：若有人拖动=敲波波
- **Round3（第三圈）**：禁止发照，直接比牌

### 8.2 合法性规则（服务端校验）

**第一圈：**

- 庄家下家：
  - 允许 pass 或 call
  - call 金额= `2 × 皮`
- 首次 raise ≥ `5 × 屁`
- 后续 raise ≥ `lastRaise`
  - 否则视为 knock_bobo

**第二圈：**

- 允许发照与说钱
- 客户端拖动 → 当作 knock_bobo

**第三圈：**

- 不允许发照/说钱
- 直接摊牌比牌

### 8.3 状态推进

- 每个动作后：
  1. 落地 actions_json
  2. 推送新 state
  3. 若本轮结束 → round++

------

## 9. 芒果/修忙/皱芒实现

### 9.1 streak 状态

保存在 `tables.mango_streak`（桌级连续性）。

### 9.2 触发判定（match 结束时）

- **修忙**：
  - 第一圈有人买牌（=至少两人未丢）
  - 第二圈无人说钱
  - 或最终无比牌结束
     → streak++（cap=3）
- **皱芒**：
  - 第二圈有人说钱
  - 但无人买牌（只剩说钱者）
     → 不产生芒果，streak=0
     → 尾大吃皮
- **发生比牌**：
   → streak=0
- **streak=3 后无人说钱**：
   → 保持 3

### 9.3 芒果注

`mangoValue = streak * 2 * 皮`
 streak=1/2/3 对应 2/4/6×皮。

------

## 10. 结算、抽成与代理分润

### 10.1 结算事务（非常关键）

在 `service/game/settle.go` 里用 DB 事务保证原子性：

1. 锁定本局参与者 wallets（`SELECT ... FOR UPDATE`）
2. 计算每人输赢 `delta`
3. 对赢家 `win_points>0`：
   - `rake = rakeRule.Apply(win_points)`
   - `net_win = win_points - rake`
4. 写 billing_logs（win/lose/rake）
5. 代理分润：
   - 若赢家绑定代理 →
      `agent_pool = rake`
      L1 40%，平台60%
      多级按 agent_rules 逐级扣
   - 写 agent_profit_logs + billing_logs(agent_share/platform_income)
6. 更新 wallets
7. 写 matches/result_json/rake_json
8. commit

### 10.2 幂等

- `matches` 有唯一 match_id
- settle 时优先查 `matches.ended_at`
  - 已结算则直接返回（避免重复回调）

------

## 11. 啵啵补码处理

结算后计算：

```
if wallet.Available < scene.MinIn*0.6 {
   need := max(scene.MinIn*0.5 - wallet.Available, 0)
   // 写入提示到 state / hall notice
}
```

前端只展示提示，补码通过充值或管理端人工补。

------

## 12. 安全与风控

### 12.1 WS 防刷

- 单连接 QPS 限制
- 非 turnSeat 玩家动作直接 reject
- 超过 N 次非法动作 → 踢出并记风控日志

### 12.2 匹配风控

- 同 IP/同网段：直接视作距离=0 不同桌
- 24h 同桌次数过高 → 降权匹配（V1.1可报警）

### 12.3 牌面与复盘隐私

- `cards_json` 按 user 加密（AES + userKey）
- 复盘 API 解密仅返回本人视角
- 对手永远匿名

------

## 13. 部署与运维

### 13.1 Docker-compose 起步

- api（含 ws）
- postgres
- redis
- nginx

### 13.2 k8s 扩展

- WS 服务水平扩展需**粘性会话/房间分片**：
  - tableId hash 到固定 pod（V1.1）
  - 或用 “房间服务 + 网关转发”

### 13.3 监控

- WS 在线数/房间数/消息P99
- 匹配成功率/平均等待时间
- 结算失败率/幂等命中次数
- 抽成收入/代理收入对账差异