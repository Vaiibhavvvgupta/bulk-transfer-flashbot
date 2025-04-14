package main

import (
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
)

func main() {
	fmt.Printf("Auth Key: 0x%x\n")

	privateKey, err := crypto.GenerateKey()
	if err != nil {
		panic(err)
	}
	privateKeyBytes := crypto.FromECDSA(privateKey)
	fmt.Printf("Auth Key: 0x%x\n", privateKeyBytes)
}