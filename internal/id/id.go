package id

import (
	"crypto/rand"
	"fmt"
)

func Generate() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("job_%x", b)
}
