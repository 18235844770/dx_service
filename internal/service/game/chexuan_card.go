package game

import (
	mrand "math/rand"
	"strings"
)

// ChexuanCard represents a single Chexuan card.
// Ranks are based on the rules:
// Level 1: RQ > R2 > R8 > R4
// Level 2: B10 = B4 = B6
// Level 3: BJ = R10 = R6 = R7
// Level 4: B5 = B7 = B8 = B9 = R3 = BK
type ChexuanCard struct {
	ID    int
	Code  string
	Rank  int    // ordering weight, higher is stronger
	Point int    // point value used by mod-10 rules
	Name  string // human readable name
	Suit  string // "red", "black", or "special"
}

// Points:
// Q=2, J=1, BK=6. Others = face value.
var chexuanCardMap = map[string]ChexuanCard{
	// Level 1
	"RQ": {ID: 1, Code: "RQ", Rank: 150, Point: 2, Name: "red_q", Suit: "red"},
	"R2": {ID: 2, Code: "R2", Rank: 140, Point: 2, Name: "red_2", Suit: "red"},
	"R8": {ID: 3, Code: "R8", Rank: 130, Point: 8, Name: "red_8", Suit: "red"},
	"R4": {ID: 4, Code: "R4", Rank: 120, Point: 4, Name: "red_4", Suit: "red"},

	// Level 2 (Rank 110)
	"B10": {ID: 5, Code: "B10", Rank: 110, Point: 0, Name: "black_10", Suit: "black"}, // 10 is 0 points usually
	"B4":  {ID: 6, Code: "B4", Rank: 110, Point: 4, Name: "black_4", Suit: "black"},
	"B6":  {ID: 7, Code: "B6", Rank: 110, Point: 6, Name: "black_6", Suit: "black"},

	// Level 3 (Rank 100)
	"BJ":  {ID: 8, Code: "BJ", Rank: 100, Point: 1, Name: "black_j", Suit: "black"},
	"R10": {ID: 9, Code: "R10", Rank: 100, Point: 0, Name: "red_10", Suit: "red"},
	"R6":  {ID: 10, Code: "R6", Rank: 100, Point: 6, Name: "red_6", Suit: "red"},
	"R7":  {ID: 11, Code: "R7", Rank: 100, Point: 7, Name: "red_7", Suit: "red"},

	// Level 4 (Rank 90)
	"B5": {ID: 12, Code: "B5", Rank: 90, Point: 5, Name: "black_5", Suit: "black"},
	"B7": {ID: 13, Code: "B7", Rank: 90, Point: 7, Name: "black_7", Suit: "black"},
	"B8": {ID: 14, Code: "B8", Rank: 90, Point: 8, Name: "black_8", Suit: "black"},
	"B9": {ID: 15, Code: "B9", Rank: 90, Point: 9, Name: "black_9", Suit: "black"},
	"R3": {ID: 16, Code: "R3", Rank: 90, Point: 3, Name: "red_3", Suit: "red"},     // Single
	"BK": {ID: 17, Code: "BK", Rank: 90, Point: 6, Name: "big_king", Suit: "special"}, // Single
}

// chexuanDeckTemplate declares 32 cards.
// 2 copies of most, 1 copy of R3 and BK.
var chexuanDeckTemplate = []string{
	// Level 1 (4 types * 2 = 8)
	"RQ", "RQ",
	"R2", "R2",
	"R8", "R8",
	"R4", "R4",

	// Level 2 (3 types * 2 = 6)
	"B10", "B10",
	"B4", "B4",
	"B6", "B6",

	// Level 3 (4 types * 2 = 8)
	"BJ", "BJ",
	"R10", "R10",
	"R6", "R6",
	"R7", "R7",

	// Level 4 (4 types * 2 + 2 singles = 10)
	"B5", "B5",
	"B7", "B7",
	"B8", "B8",
	"B9", "B9",
	"R3", // Single
	"BK", // Single
}

// NewChexuanDeck returns a shuffled deck of Chexuan card codes.
func NewChexuanDeck() []string {
	deck := make([]string, len(chexuanDeckTemplate))
	copy(deck, chexuanDeckTemplate)
	mrand.Shuffle(len(deck), func(i, j int) {
		deck[i], deck[j] = deck[j], deck[i]
	})
	return deck
}

func chexuanCardByCode(code string) (ChexuanCard, bool) {
	c, ok := chexuanCardMap[strings.ToUpper(code)]
	return c, ok
}

// ToPokerCode converts a Chexuan card code (e.g., "RQ", "B10") to a standard poker code (e.g., "Qh", "Ts").
// Red cards map to Hearts (h).
// Black cards map to Spades (s).
// BK (Big King) maps to As (Spade Ace).
func ToPokerCode(cxCode string) string {
	cxCode = strings.ToUpper(cxCode)
	switch cxCode {
	// Level 1 (Red -> Hearts)
	case "RQ":
		return "Qh"
	case "R2":
		return "2h"
	case "R8":
		return "8h"
	case "R4":
		return "4h"

	// Level 2 (Black -> Spades)
	case "B10":
		return "Ts"
	case "B4":
		return "4s"
	case "B6":
		return "6s"

	// Level 3 (Mix)
	case "BJ":
		return "Js" // Black J
	case "R10":
		return "Th" // Red 10
	case "R6":
		return "6h" // Red 6
	case "R7":
		return "7h" // Red 7

	// Level 4 (Mix)
	case "B5":
		return "5s"
	case "B7":
		return "7s"
	case "B8":
		return "8s"
	case "B9":
		return "9s"
	case "R3":
		return "3h" // Red 3
	case "BK":
		return "As" // Big King -> Spade Ace (for visual prominence)
	
	default:
		return cxCode
	}
}
