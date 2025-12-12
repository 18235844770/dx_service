# 扯旋 (Chexuan) 游戏详细代码改造方案

本文档为 `dx-service` 和 `dx-app` 提供详细的步骤级代码改造指南，旨在实现扯旋 (Chexuan) 玩法。

## 一、 后端改造步骤 (`dx-service`)

后端核心改造集中在 `internal/service/game/` 目录下。我们将采用逐步替换/扩展的方式，引入扯旋逻辑。

### 步骤 1: 创建扯旋核心库 (`pkg/chexuan` 或 `internal/service/game/chexuan`)

建议在 `internal/service/game` 下新建 `chexuan` 子目录（如果希望逻辑解耦），或者直接在 `game` 包中增加前缀文件。此处采用在 `internal/service/game` 下新增 `chexuan_*.go` 文件的方式。

#### 1.1 定义牌与牌库 (`internal/service/game/chexuan_card.go`)
**目标**: 实现 32 张牌的定义与初始化。

- [ ] **定义常量**:
  - `CardID` (1-32)
  - 牌名枚举 (RedQ, Red2, BigKing...)
- [ ] **结构体 `ChexuanCard`**:
  ```go
  type ChexuanCard struct {
      ID    int
      Rank  int // 用于排序的大小权重 (丁皇最大)
      Point int // 用于点数计算 (个位比大小)
      Name  string
      Suit  string // "red", "black"
  }
  ```
- [ ] **初始化函数 `NewChexuanDeck()`**:
  - 返回乱序后的 `[]string` 或 `[]ChexuanCard`。
  - **牌表映射**: 建立 `string` (如 "RQ") 到 `ChexuanCard` 的映射表。
  - **32张牌构成**:
    - **红**: Q(2张), 2(2张), 8(2张), 4(2张) ... (需按规则确定的 32 张牌填入)
    - **特殊**: 大王, 红3.

#### 1.2 实现牌型评估算法 (`internal/service/game/chexuan_algo.go`)
**目标**: 实现牌组大小比较与分牌算法。

- [ ] **函数 `GetCardPoint(c ChexuanCard) int`**: 返回单张点数。
- [ ] **函数 `EvaluateGroup(c1, c2 ChexuanCard) int64`**:
  - 输入两张牌，返回一个 `int64` 分值。
  - **分值设计**:
    - **Tier 1 (至尊/对子/特殊组合)**: 赋予高分段 (e.g. `10,000,000` + Rank)。
    - **Tier 2 (点数)**: `(p1+p2)%10 * 100`。
    - **Tier 3 (单牌最大)**: `Max(rank1, rank2)`.
  - **判定顺序**: 丁皇 > 天皇 > ... > 杂对 > 牌型(三花) > 点数 > 单牌。
- [ ] **函数 `BestSplit(cards []string) (head, tail []string, score int64)`**:
  - 遍历 3 张或 4 张牌的所有 $C(n,2)$ 组合。
  - 找出 `Head >= Tail` 的最优组合。
  - 如果所有组合都 `Head < Tail` (倒把)，则按规则处理（通常自动输或强制最小组合）。

### 步骤 2: 改造运行时数据结构 (`internal/service/game/runtime.go`)

#### 2.1 扩展 `TableRuntime` 结构体
- [ ] 在 `TableRuntime` 中增加字段:
  ```go
  chexuanMode  bool // 是否启用扯旋模式 (可由 Scene 配置决定)
  mangoStreak  int  // 芒果数 (0-3)
  round1Bet    bool // R1 是否有有效下注 (非 Pass/Check)
  round2Bet    bool // R2 是否有有效下注
  round2Knock  bool // R2 是否有人敲波波
  deck         []string // 保持通用，但内容变为 Chexuan 牌
  ```

#### 2.2 修改 `newTableRuntime`
- [ ] 初始化时检查 `Scene` 配置（如 `GameType`），设置 `chexuanMode`。
- [ ] 如果是扯旋模式，`initDeckLocked` 调用扯旋专用洗牌逻辑。

### 步骤 3: 改造游戏流程控制 (`internal/service/game/runtime.go`)

#### 3.1 改造发牌逻辑 (`dealCardsLocked`)
**目标**: 支持 2-1-1 发牌节奏。

- [ ] 修改 `dealCardsLocked`，根据 `rt.round` 决定发牌数量:
  ```go
  count := 0
  if rt.round == 0 { count = 2 }      // Start (其实是 Round 0 结束进 Round 1 前)
  else if rt.round == 1 { count = 1 } // Round 1 结束进 Round 2 前
  else if rt.round == 2 { count = 1 } // Round 2 结束进 Round 3 前
  ```
- [ ] 确保发牌追加到 `seat.cards` 而非重置（除 Round 0）。

#### 3.2 改造下注动作 (`handleRaiseLocked` / `handleTurnActionLocked`)
**目标**: 实现 R1/R2 的特殊限制。

- [ ] **R1 限制**:
  - 在 `handleRaiseLocked` 中，若 `rt.round == 1`:
    - 检查 `amount >= 5 * MinUnit`。
    - 庄家下家首注检查: `amount == 2 * BasePi` (如果是跟注/开注)。
- [ ] **R2 限制 (敲波波)**:
  - 处理 `knock_bobo` action。
  - 逻辑: 视为 Raise 到某个上限 (或 All-in)，设置 `rt.round2Knock = true`。
  - 触发后可能直接跳过后续 Raise，只能 Call/Fold。

#### 3.3 改造回合流转 (`advanceRoundLocked`)
**目标**: 插入“修忙”检测。

- [ ] 在回合结束（跳转下一轮前）:
  - **Check R1 -> R2**: 记录 R1 是否有 Bet。
  - **Check R2 -> R3**:
    - 如果 R1 有 Bet 但 R2 无人 Raise (只有 Check/Call) -> **修忙**。
    - 如果 R2 有 Raise 但无人 Call (所有人 Fold) -> **尾大吃皮** (需在 Settle 处理)。

### 步骤 4: 重写结算逻辑 (`internal/service/game/settle.go`)

#### 4.1 实现多人比牌 (`determineWinnersAndSettleLocked`)
**目标**: 替换现有的单 Pot 逻辑。

- [ ] **预处理**:
  - 对每个 Active Player 调用 `BestSplit`，得到 `HeadScore`, `TailScore`。
- [ ] **排序**:
  - 按 `HeadScore` 降序排列所有玩家。
  - 若 `HeadScore` 相同，按 `Head` 中最大单牌 Rank 排序。
  - 若还相同，按 `TailScore` 排序。
- [ ] **循环结算**:
  - 创建临时账本 `map[int64]int64`。
  - **双重循环**:
    ```go
    for i := 0; i < len(players); i++ {
        winner := players[i]
        for j := i + 1; j < len(players); j++ {
            loser := players[j]
            // Compare(winner, loser)
            // 计算赢钱 amount = Min(winner.Bet, loser.Bet) * Rate? 
            // 通常扯旋是互博，赢家拿走输家筹码，不超过自己筹码。
        }
    }
    ```
- [ ] **应用保护规则**:
  - **头大保命**: 检查 `players[0]` (头最大者) 的净输赢。如果 `Net < 0`，修正为 `Max(-Mango - BasePi, Net)`。
  - **尾大吃皮**: 如果标记为“尾大吃皮”局，直接判最后 Raise 者赢取底皮。

#### 4.2 芒果更新
- [ ] 如果是修忙局: `rt.mangoStreak++` (Max 3)。
- [ ] 如果是正常对抗局: `rt.mangoStreak = 0`。
- [ ] 保存 `mangoStreak` 到 DB `Table` 记录。

---

## 二、 前端改造步骤 (`dx-app`)

### 步骤 1: 资源准备
- [ ] **图片资源**: 准备 32 张扯旋牌图片，命名规范化 (e.g. `chexuan_r2.png`, `chexuan_bk.png`)。
- [ ] **声音资源**: 加注、敲波波音效。

### 步骤 2: 适配状态展示 (`src/store/table.ts` & `src/types/game.ts`)
- [ ] **Type 定义**:
  - 更新 `SeatDTO`，增加 `split?: { head: string[], tail: string[] }`。
  - 更新 `TableState`，增加 `mangoStreak`。
- [ ] **Store**:
  - 处理 `state` 更新时对 `mangoStreak` 的存储。

### 3. 组件改造

#### 3.1 牌面组件 (`src/components/table/CardsHand.vue`)
- [ ] **支持多张牌**: 修改布局，支持 2, 3, 4 张牌的排列。
- [ ] **分牌展示**: 结算状态下，将 4 张牌分为 上下两组 或 左右两组 显示 (头/尾)。

#### 3.2 操作栏 (`src/components/table/ActionBar.vue`)
- [ ] **新增按钮**: "敲" (Knock)。
- [ ] **显示逻辑**: 仅在 `allowedActions` 包含 `knock_bobo` 时显示。

#### 3.3 桌面信息 (`src/pages/table/index.vue`)
- [ ] **芒果指示器**: 在桌面中央或角落显示当前是 "1忙", "2忙" 还是 "3忙"。
- [ ] **发照交互**: R1 阶段，允许点击自己盖着的牌查看 (如果后端发的是暗牌)。

### 4. 结算弹窗 (`src/pages/table/index.vue` -> ResultOverlay)
- [ ] **重构**: 从简单的 `+100` 改为详细列表。
- [ ] **列表项**:
  - 玩家头像/名字。
  - 牌型展示: [头牌型] [尾牌型]。
  - 输赢结果: 赢/输/平 (以及具体金额)。

---

## 三、 测试用例规划

### 后端单元测试
1.  **TestEvaluate**: 测试丁皇、天皇、杂对、点数的大小比较是否正确。
2.  **TestSplit**: 测试 `BestSplit` 是否能正确找出最优解，且符合 Head >= Tail。
3.  **TestSettle**: 模拟 3 人局，构造特定牌型，验证循环结算金额是否正确（特别是头大保命）。

### 集成测试 (Game Flow)
1.  **修忙流程**:
    - P1 Bet -> P2 Fold -> P3 Fold (不成立，直接赢)。
    - P1 Bet -> P2 Call -> R2 Check -> R2 Check -> Settle (成立修忙)。
    - 验证 MangoStreak +1。
2.  **敲波波流程**:
    - R2 P1 Knock -> 验证 Log 和状态变化 -> 进入 Settle。
