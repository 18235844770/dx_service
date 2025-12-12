package game

import (
	"sort"
	"strconv"
)

// Card represents a poker card
// Format: Rank + Suit (e.g., "As", "Td", "2c")
// Ranks: 2, 3, 4, 5, 6, 7, 8, 9, T, J, Q, K, A
// Suits: s (spades), h (hearts), d (diamonds), c (clubs)

type HandRank int

const (
	HighCard HandRank = iota
	Pair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush
	RoyalFlush
)

type ParsedCard struct {
	RankValue int
	Suit      rune
	Original  string
}

func parseCard(card string) ParsedCard {
	if len(card) < 2 {
		return ParsedCard{}
	}
	r := card[0]
	s := rune(card[1])
	val := 0
	switch r {
	case '2', '3', '4', '5', '6', '7', '8', '9':
		val, _ = strconv.Atoi(string(r))
	case 'T':
		val = 10
	case 'J':
		val = 11
	case 'Q':
		val = 12
	case 'K':
		val = 13
	case 'A':
		val = 14
	}
	return ParsedCard{RankValue: val, Suit: s, Original: card}
}

// EvaluateHand returns a score for comparing hands.
// Higher score wins.
// For simplicity in this example, we implement a basic high card comparison
// suitable for "Big 2" or simplified Poker.
// Real Texas Hold'em needs complex 5-card evaluation from 7 cards.
// Assuming "Mango" style might just compare 2 cards or specific rules.
// Here we implement a generic 2-card evaluator for High Card / Pair.
func EvaluateHand(cards []string) int64 {
	if len(cards) == 0 {
		return 0
	}
	parsed := make([]ParsedCard, len(cards))
	for i, c := range cards {
		parsed[i] = parseCard(c)
	}
	
	// Sort descending by rank
	sort.Slice(parsed, func(i, j int) bool {
		return parsed[i].RankValue > parsed[j].RankValue
	})

	// Basic Pair logic
	if len(parsed) >= 2 {
		if parsed[0].RankValue == parsed[1].RankValue {
			// Pair: Score = 1,000,000 * Rank
			return 1_000_000 * int64(parsed[0].RankValue)
		}
	}

	// High Card: Score = Rank1 * 100 + Rank2
	score := int64(0)
	if len(parsed) > 0 {
		score += int64(parsed[0].RankValue) * 100
	}
	if len(parsed) > 1 {
		score += int64(parsed[1].RankValue)
	}
	return score
}

