package random

import (
	"crypto/rand"
	"math/big"
)

const digits = "0123456789"
const letters = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func Numeric(length int) string {
	return pickFromSet(digits, length)
}

func Code(length int) string {
	return pickFromSet(letters, length)
}

func pickFromSet(set string, length int) string {
	if length <= 0 {
		return ""
	}
	max := big.NewInt(int64(len(set)))
	runes := make([]byte, length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			runes[i] = set[0]
			continue
		}
		runes[i] = set[n.Int64()]
	}
	return string(runes)
}
