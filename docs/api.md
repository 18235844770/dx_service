# 接口文档（V1）

> 依据 `README.md` 设计整理，当前实现版本中部分接口仍为占位（待补全 JWT、业务逻辑、数据验证等）。如无特殊说明，所有接口均为 JSON 请求/响应，响应体包含 `application/json`，时间使用 ISO8601。

## 1. 认证 Auth

### 1.1 `POST /dxService/v1/auth/sms/send`

- **描述**：发送短信验证码（目前日志里只打印脱敏信息，不会真正下发短信）。
- **请求体**
  ```json
  {
    "phone": "19900000001"
  }
  ```
- **响应**
  ```json
  { "message": "code sent" }
  ```
- **错误码**：`400`（手机号格式非法）。

### 1.2 `POST /dxService/v1/auth/sms/login`

- **描述**：通过手机号 + 验证码登录；首次登录自动建号，可附带 `inviteCode` 绑定代理。若用户状态为 `banned` 将拒绝登录。
- **请求体**
  ```json
  {
    "phone": "19900000001",
    "code": "123456",
    "inviteCode": "ABC123"   // 可选
  }
  ```
- **响应**
  ```json
  {
    "token": "jwt-token",
    "expireAt": "2025-11-26T09:00:00Z",
    "user": {
      "id": 1001,
      "phone": "19900000001",
      "nickname": null,
      "inviteCode": "XYZ123",
      "status": "normal",
      ...
    }
  }
  ```
- **错误码**：
  - `400`：手机号/验证码非法、邀请码不存在或用户已绑定代理。
  - `403`：用户被封禁。
  - `410`：验证码已过期或尚未发送。

## 2. 用户 User

> 需在 Header 携带 `Authorization: Bearer <token>`。

### 2.1 `GET /dxService/v1/user/profile`

- **描述**：获取当前登录用户资料。
- **响应**
  ```json
  {
    "data": {
      "id": 1001,
      "phone": "19900000001",
      "nickname": "玩家A",
      "avatar": "https://...",
      "locationCity": "上海",
      "gpsLat": 31.23,
      "gpsLng": 121.47,
      "status": "normal",
      "inviteCode": "XYZ123",
      "bindAgentId": 9001
    }
  }
  ```

### 2.2 `PUT /dxService/v1/user/profile`

- **请求体**（字段全为可选）
  ```json
  {
    "nickname": "新昵称",
    "avatar": "https://...",
    "locationCity": "上海",
    "gpsLat": 31.23,
    "gpsLng": 121.47
  }
  ```
- **响应**：同 GET，返回更新后的 `data`。
- **错误码**：`401`（未带 token） / `400`（参数校验失败）。

## 3. 大厅 / 场次 / 钱包

### 3.1 `GET /dxService/v1/scenes`

- **描述**：查询所有可用场次。
- **响应**
  ```json
  {
    "data": [
      {
        "id": 10,
        "name": "经典 6 人桌",
        "seatCount": 6,
        "minIn": 3000,
        "maxIn": 20000,
        "basePi": 100,
        "minUnitPi": 20,
        "mangoEnabled": true,
        "boboEnabled": true,
        "distanceThresholdM": 1000,
        "rakeRuleId": 1,
        "createdAt": "..."
      }
    ]
  }
  ```

### 3.2 `GET /dxService/v1/wallet?userId=<id>`

- **描述**：查询用户钱包。暂未接入 JWT，直接使用 Query 参数。
- **响应**
  ```json
  {
    "data": {
      "userId": 1001,
      "balanceTotal": 100000,
      "balanceAvailable": 100000,
      "balanceFrozen": 0,
      "totalRecharge": 0,
      "totalWin": 0,
      "totalConsume": 0,
      "totalRake": 0,
      "updatedAt": "..."
    }
  }
  ```

## 4. 匹配 Match

> 需在 Header 携带 `Authorization: Bearer <token>`，服务端将使用 token 中的 `userId`。
> 超过 3 分钟仍未匹配成功会自动退出队列。

### 4.1 `POST /dxService/v1/match/join`

- **描述**：加入匹配队列。
- **请求体**
  ```json
  {
    "sceneId": 10,
    "buyIn": 5000,
    "gpsLat": 31.23,
    "gpsLng": 121.47
  }
  ```
- **响应**
  ```json
  {
    "code": 200,
    "data": {
      "queueId": "1001",
      "status": "queued"
    },
    "msg": ""
  }
  ```
- **错误码**：
  - `401`：缺少或无效 token。
  - `404`：场次不存在。
  - `400`：买入不合法（返回“买入金额不合法”）/ 余额不足（返回“余额不足”）。
  - `409`：用户已在队列中。
  - `429`：入队处理冲突（Queue lock 未释放）。
  - `500`：其它服务端异常。

### 4.2 `POST /dxService/v1/match/cancel`

- **描述**：取消匹配。
- **请求体**
  ```json
  {
    "sceneId": 10
  }
  ```
- **响应**
  ```json
  { "status": "cancelled" }
  ```

### 4.3 `GET /dxService/v1/match/status?sceneId=<scene>`

- **描述**：轮询匹配状态。
- **响应示例**
  ```json
  { "status": "queued", "sceneId": 10, "joinedAt": "2025-11-25T..." }
  ```
  ```json
  { "status": "matched", "sceneId": 10, "tableId": 88, "matchId": 123 }
  ```
  ```json
  { "status": "idle", "sceneId": 10 }
  ```

## 5. 记录 / 复盘（规划）

- `GET /dxService/v1/match/list`
- `GET /dxService/v1/match/{id}/replay`

> 需要实现数据库查询、复盘视角下发等逻辑。

## 6. 代理 / 分润（规划）

- `GET /dxService/v1/agent/dashboard`
- `GET /dxService/v1/agent/invitees`
- `GET /dxService/v1/agent/profits`

## 7. 管理端（规划）

### 7.0 `POST /admin/auth/login`
- 描述：管理员账号密码登录，返回管理端 JWT。
- 请求体：
  ```json
  {
    "username": "admin",
    "password": "Admin@123456"
  }
  ```
- 响应：
  ```json
  {
    "token": "jwt-token",
    "expireAt": "2025-11-26T09:00:00Z",
    "admin": {
      "id": 1,
      "username": "admin",
      "displayName": "admin",
      "status": "active",
      "lastLoginAt": "2025-11-25T09:00:00Z"
    }
  }
  ```
- 错误码：
  - `401`：账号不存在或密码错误。
  - `403`：账号被禁用。

> 默认账号及密码来源于 `config.yaml` 中的 `admin.defaultUsername/defaultPassword`，首次部署后务必立即修改密码。

### 7.1 `GET /admin/scenes`
- 描述：分页获取所有场次配置
- Query：
  - `page`、`size`
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {"id":1,"name":"经典6人桌","seatCount":6,"minIn":3000,"maxIn":20000,"basePi":100,"minUnitPi":20,"mangoEnabled":true,"boboEnabled":true,"distanceThresholdM":1000,"rakeRuleId":1,"createdAt":"..."}
      ],
      "total": 1
    },
    "msg": ""
  }
  ```

### 7.2 `POST /admin/scenes`
- 描述：创建新场次
- 请求体：
  ```json
  {
    "name": "高额桌",
    "seatCount": 6,
    "minIn": 10000,
    "maxIn": 50000,
    "basePi": 200,
    "minUnitPi": 40,
    "mangoEnabled": true,
    "boboEnabled": false,
    "distanceThresholdM": 2000,
    "rakeRuleId": 2
  }
  ```
- 响应：`{code:200,data:{id:...},msg:""}`
- 错误码：`400` 参数非法；`409` 场次名称重复

### 7.3 `PUT /admin/scenes/{id}`
- 描述：更新场次配置
- 请求体同创建
- 响应：`{code:200,data:{},msg:""}`

### 7.4 `GET /admin/rake_rules`
- 描述：获取抽佣规则列表
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {"id":1,"type":"ratio","configJson":{"ratio":0.05,"cap":2000},"createdAt":"..."}
      ],
      "total":1
    },
    "msg":""
  }
  ```

### 7.5 `POST /admin/rake_rules`
- 描述：创建抽佣规则
- 请求体示例：
  ```json
  {
    "type": "ratio",
    "configJson": {
      "ratio": 0.05,
      "cap": 2000
    }
  }
  ```
- 响应：`{code:200,data:{id:...},msg:""}`

### 7.6 `PUT /admin/rake_rules/{id}`
- 描述：更新抽佣规则
- 请求体同上

### 7.7 `GET /admin/agent_rules`
- 描述：获取代理分润规则列表
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {"id":1,"maxLevel":3,"levelRatiosJson":{"L1":0.4,"L2":0.1,"L3":0.05},"basePlatformRatio":0.45}
      ]
    },
    "msg":""
  }
  ```

### 7.8 `POST /admin/agent_rules`
- 描述：创建代理分润规则
- 请求体：
  ```json
  {
    "maxLevel": 3,
    "levelRatiosJson": {"L1":0.4,"L2":0.1,"L3":0.05},
    "basePlatformRatio": 0.45
  }
  ```

### 7.9 `PUT /admin/agent_rules/{id}`
- 描述：更新代理分润规则

### 7.10 `GET /admin/users`
- 描述：分页查询用户列表，可按状态、手机号、邀请码、绑定代理过滤
- Query：`page/size/status/phone/inviteCode/agentId`
- 响应：`{code:200,data:{items:[...],total:1,page:1,size:20},msg:""}`

### 7.11 `GET /admin/users/{id}`
- 描述：获取单个用户详情（不存在返回 `404`）
- 响应：`{code:200,data:{user:{...}},msg:""}`

### 7.12 `PUT /admin/users/{id}/ban`
- 描述：封禁或解封用户
- 请求体：
  ```json
  {
    "status": "banned",      // "normal" 解封
    "reason": "违规操作"
  }
  ```
- 响应：`{code:200,data:{user:{...}},msg:""}`；若用户不存在 `404`

### 7.13 `GET /admin/matches`
- 描述：查询对局列表
- Query：
  - `sceneId?`
  - `tableId?`
  - `status?` (waiting/playing/ended)
  - `page/size`
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {"id":101,"tableId":20,"sceneId":1,"createdAt":"...","endedAt":"...","resultJson":{...}}
      ],
      "total": 42
    },
    "msg": ""
  }
  ```

## 8. WebSocket 房间

### 8.1 连接
```
wss://host/ws/table/{tableId}?token=...
```

### 8.2 消息格式
```json
// client -> server
{ "type": "action", "seq": 123, "data": { ... } }

// server -> client
{ "type": "state", "seq": 124, "data": { ... } }
{ "type": "error", "seq": 125, "data": { "code": 4001, "message": "..." } }
```

### 8.3 动作枚举
`pass`, `call`, `raise`, `knock_bobo`, `fold`, `ready`, `rejoin`, `ping`

### 8.4 状态下发（示例）
```json
{
  "round": 1,
  "turnSeat": 2,
  "lastRaise": 50,
  "mangoStreak": 2,
  "countdown": 12,
  "allowedActions": ["pass","call","raise","fold"],
  "seats": [
    { "seat": 1, "alias": "玩家A", "chips": 900, "status": "alive" },
    { "seat": 2, "alias": "玩家B", "chips": 1100, "status": "alive" }
  ],
  "myCards": ["**","**","**"],
  "logs": [ ... ]
}
```

---

- **未完成功能**：代理/管理端业务接口、复盘/房间状态机等仍待实现。
- **更新**：管理端登录已实现，业务接口仍在规划阶段。
- **使用建议**：当前阶段可通过 DB 插入临时用户，并直接在接口中传 `userId` 测试；后续接入 JWT 后再统一鉴权。

