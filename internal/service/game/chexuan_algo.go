package game

import (
	"sort"
)

func GetCardPoint(c ChexuanCard) int {
	return c.Point
}

// EvaluateGroup scores a 2-card combination.
// Higher scores win. Score tiers:
//
//	10,000,000 + specialWeight -> special hands (dinghuang, nai gou...)
//	 9,000,000 + rank          -> pairs
//	       points*100 + rank   -> default (points mod 10, then single-card rank)
func EvaluateGroup(c1, c2 ChexuanCard) int64 {
	key := normalizePairKey(c1, c2)
	if w, ok := chexuanSpecialWeights[key]; ok {
		return 10_000_000 + int64(w)
	}
	if c1.Code == c2.Code {
		return 9_000_000 + int64(maxInt(c1.Rank, c2.Rank))
	}
	points := (c1.Point + c2.Point) % 10
	high := maxInt(c1.Rank, c2.Rank)
	return int64(points*100 + high)
}

// BestSplit selects the best head/tail split for 3 or 4 cards.
// Returns head, tail, score, and whether the split is valid (head >= tail).
// If no valid split exists (all are daoba), it returns the best "invalid" split with isValid=false.
func BestSplit(cards []string) (head, tail []string, score int64, isValid bool) {
	n := len(cards)
	if n < 2 {
		return nil, nil, 0, false
	}
	if n == 2 {
		// Only 2 cards -> Head=pair, Tail=empty. Always valid for 2 cards if logic allows.
		// But usually we need 4 cards to split.
		// For 2 cards, we treat as Head only.
		return cards, nil, evaluatePairScore(cards), true
	}

	bestValidScore := int64(-1)
	var bestValidHead, bestValidTail []string

	bestOverallScore := int64(-1)
	var bestOverallHead, bestOverallTail []string

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			headCodes := []string{cards[i], cards[j]}
			tailCodes := make([]string, 0, n-2)
			for k, c := range cards {
				if k == i || k == j {
					continue
				}
				tailCodes = append(tailCodes, c)
			}

			headScore := evaluatePairScore(headCodes)
			tailScore := evaluatePairScore(tailCodes)
			
			// Compare Head vs Tail using specific logic (Score > Score or MaxRank > MaxRank)
			// Simple int64 comparison works if scores are tiered correctly.
			isSplitValid := headScore >= tailScore
			// If scores equal, check max rank
			if headScore == tailScore {
				hMax := chexuanHeadMaxRank(headCodes)
				tMax := chexuanHeadMaxRank(tailCodes)
				if hMax < tMax {
					isSplitValid = false
				}
			}

			total := headScore*1_000_000 + tailScore

			if isSplitValid && total > bestValidScore {
				bestValidScore = total
				bestValidHead = append([]string(nil), headCodes...)
				bestValidTail = append([]string(nil), tailCodes...)
			}
			if total > bestOverallScore {
				bestOverallScore = total
				bestOverallHead = append([]string(nil), headCodes...)
				bestOverallTail = append([]string(nil), tailCodes...)
			}
		}
	}

	if bestValidScore >= 0 {
		return bestValidHead, bestValidTail, bestValidScore, true
	}
	// Daoba (Invalid split)
	return bestOverallHead, bestOverallTail, bestOverallScore, false
}

// Higher weight means stronger special hand within the special tier.
// 丁皇＞对子＞奶狗＞天杠＞地杠＞天关9＞地关9＞人牌9＞和五9＞长二9＞虎头9
var chexuanSpecialWeights = map[string]int{
	// 至尊类
	"BK+R3": 900, // 丁皇 (Ding Huang)

	// 特殊组合（顺序越高，权重越大）
	// 奶狗 (Nai Gou): RQ + 9
	// Since we only have B9 (Black 9), key is B9+RQ
	"B9+RQ": 850,

	// 天杠 (Tian Gang): RQ + 8
	"B8+RQ": 840, "R8+RQ": 840,

	// 地杠 (Di Gang): R2 + 8
	"B8+R2": 830, "R8+R2": 830,

	// 天关 (Tian Guan): RQ + 7
	"B7+RQ": 820, "R7+RQ": 820,

	// 地关 (Di Guan): R2 + 7
	"B7+R2": 810, "R7+R2": 810,

	// 人牌 (Ren Pai): Red 8 + J (BJ)
	"BJ+R8": 800,

	// 和五 (He Wu): Red 4 + 5 (B5)
	"B5+R4": 790,

	// 长二 (Chang Er): Black 4 + 5 (B5)
	"B4+B5": 780,

	// 虎头 (Hu Tou): Black 8 + J (BJ)
	"B8+BJ": 770,
}

func normalizePairKey(c1, c2 ChexuanCard) string {
	codes := []string{c1.Code, c2.Code}
	sort.Strings(codes)
	return codes[0] + "+" + codes[1]
}

func evaluatePairScore(pair []string) int64 {
	if len(pair) < 2 {
		return 0
	}
	c1, ok1 := chexuanCardByCode(pair[0])
	c2, ok2 := chexuanCardByCode(pair[1])
	if !ok1 || !ok2 {
		return 0
	}
	return EvaluateGroup(c1, c2)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
