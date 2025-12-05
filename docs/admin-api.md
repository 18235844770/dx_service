# 管理端 API 文档（V1）

> 面向后台运营/风控同学的 HTTP 接口说明。所有接口均返回 `application/json`，字段遵循驼峰命名。除登录接口外，其余均需在 `Authorization` Header 中携带管理员 JWT：`Authorization: Bearer <token>`。

---

## 0. 账号与鉴权

### 0.1 `POST /admin/auth/login`

- **描述**：账号 + 密码登录，成功后返回管理端专用 JWT。
- **默认账号**：读取自 `config.yaml -> admin.defaultUsername/defaultPassword`，示例为 `admin / Admin@123456`。首次部署后请务必修改密码或在配置中覆盖默认凭据。
- **请求体**
  ```json
  {
    "username": "admin",
    "password": "Admin@123456"
  }
  ```
- **响应**
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
- **错误码**
  - `401`：账号不存在或密码错误。
  - `403`：账号被禁用。

---

## 1. 场次管理

### 1.1 `GET /admin/scenes`

- 描述：分页获取所有场次配置。
- Query 参数：`page`、`size`。
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {
          "id": 1,
          "name": "经典6人桌",
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
      ],
      "total": 1
    },
    "msg": ""
  }
  ```

### 1.2 `POST /admin/scenes`

- 描述：创建新场次。
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
- 响应：`{ "code": 200, "data": { "id": 2 }, "msg": "" }`
- 错误码：`400` 参数非法；`409` 场次名称重复。

### 1.3 `PUT /admin/scenes/{id}`

- 描述：更新指定场次配置，Body 同创建。
- 响应：`{ "code": 200, "data": {}, "msg": "" }`
- 错误码：`404` 场次不存在；`400/409` 同创建。

---

## 2. 抽佣规则

### 2.1 `GET /admin/rake_rules`

- 描述：获取抽佣规则列表。
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {
          "id": 1,
          "type": "ratio",
          "configJson": { "ratio": 0.05, "cap": 2000 },
          "createdAt": "..."
        }
      ],
      "total": 1
    },
    "msg": ""
  }
  ```

### 2.2 `POST /admin/rake_rules`

- 描述：创建抽佣规则。
- 请求体：
  ```json
  {
    "type": "ratio",
    "configJson": {
      "ratio": 0.05,
      "cap": 2000
    }
  }
  ```
- 响应：`{ "code": 200, "data": { "id": 5 }, "msg": "" }`

### 2.3 `PUT /admin/rake_rules/{id}`

- 描述：更新抽佣规则，Body 同创建。
- 响应：`{ "code": 200, "data": {}, "msg": "" }`
- 错误码：`404` 规则不存在；`400` 参数非法。

---

## 3. 代理分润规则

### 3.1 `GET /admin/agent_rules`

- 描述：获取代理分润规则列表。
- Query 参数：`page`、`size`（默认 `1/20`，`size` 最大 100）。
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {
          "id": 1,
          "maxLevel": 3,
          "levelRatiosJson": { "L1": 0.4, "L2": 0.1, "L3": 0.05 },
          "basePlatformRatio": 0.45
        }
-      ],
      "total": 1,
      "page": 1,
      "size": 20
    },
    "msg": ""
  }
  ```

### 3.2 `POST /admin/agent_rules`

- 描述：创建代理分润规则。
- 请求体：
  ```json
  {
    "maxLevel": 3,
    "levelRatiosJson": { "L1": 0.4, "L2": 0.1, "L3": 0.05 },
    "basePlatformRatio": 0.45
  }
  ```
- 约束：`basePlatformRatio` 需在 `[0, 1]` 区间；`levelRatiosJson` 必须是合法 JSON。
- 响应：`{ "code": 200, "data": { "id": 3 }, "msg": "" }`

### 3.3 `PUT /admin/agent_rules/{id}`

- 描述：更新代理分润规则，Body 同创建。
- 响应：`{ "code": 200, "data": { ... }, "msg": "" }`
- 错误码：`404` 规则不存在；`400` 参数非法。
- 响应：`{ "code": 200, "data": {}, "msg": "" }`

---

## 4. 用户风控

### 4.1 `GET /admin/users`

- 描述：分页查询用户列表。
- Query 参数：
  - `page`、`size`（默认 `1/20`，`size` 最大 100）
  - `status`（可选，`normal/banned`）
  - `phone`（可选，手机号模糊匹配）
  - `inviteCode`（可选，邀请码模糊匹配）
  - `agentId`（可选，按绑定代理 ID 精确过滤）
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {
          "id": 1001,
          "phone": "19900000001",
          "nickname": "玩家A",
          "status": "normal",
          "bindAgentId": 9001,
          "inviteCode": "XYZ123",
          "createdAt": "...",
          "updatedAt": "..."
        }
      ],
      "total": 1,
      "page": 1,
      "size": 20
    },
    "msg": ""
  }
  ```

### 4.2 `GET /admin/users/{id}`

- 描述：获取单个用户详情。
- 响应：同列表单条记录；若用户不存在返回 `404`。

### 4.3 `PUT /admin/users/{id}/ban`

- 描述：封禁或解封用户。
- 请求体：
  ```json
  {
    "status": "banned",
    "reason": "违规操作"
  }
  ```
- 响应：`{ "code": 200, "data": { "user": { ... } }, "msg": "" }`
- 错误码：`404` 用户不存在。

### 4.4 `PUT /admin/users/{id}/wallet`

- 描述：设置用户钱包余额（覆盖当前值）。
- 请求体（两个字段至少填写一个，单位：积分）
  ```json
  {
    "balanceAvailable": 120000,
    "balanceFrozen": 0
  }
  ```
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "wallet": {
        "userId": 1001,
        "balanceAvailable": 120000,
        "balanceFrozen": 0,
        "balanceTotal": 120000,
        "updatedAt": "..."
      }
    },
    "msg": ""
  }
  ```
- 错误码：`400` 参数非法（金额为负 / 缺少字段）；`404` 用户不存在（若后续增加校验）。

---

## 5. 对局与审计

### 5.1 `GET /admin/matches`

- 描述：查询对局列表，支持条件过滤。
- Query 参数：
  - `sceneId`（可选）
  - `tableId`（可选）
  - `status`：`waiting/playing/ended`
  - `page`、`size`
- 响应：
  ```json
  {
    "code": 200,
    "data": {
      "items": [
        {
          "id": 101,
          "tableId": 20,
          "sceneId": 1,
          "createdAt": "...",
          "endedAt": "...",
          "resultJson": { "...": "..." }
        }
      ],
      "total": 42
    },
    "msg": ""
  }
  ```

---

## 6. 后续规划

- 管理员账号 CRUD、密码重置 / 二次验证。
- 管理端操作审计日志。
- 复盘详情、房间状态机控制台。

> 如需新增接口或字段，请在该文档中追加对应章节并同步客户端/前端同学。 

