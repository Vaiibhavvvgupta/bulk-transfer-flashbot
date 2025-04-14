package eth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/sirupsen/logrus"
)

// BundleRequest defines the transfer payload
type BundleRequest struct {
	ParentAddress string          `json:"parentAddress"`
	Transfers     []TransferEntry `json:"transfers"`
}

type TransferEntry struct {
	PrivateKey string `json:"privateKey"`
	EthAmount  string `json:"ethAmount"`
	UsdtAmount string `json:"usdtAmount"`
}

// USDT ABI (minimal)
var usdtABI, _ = abi.JSON(strings.NewReader(`[{"constant":false,"inputs":[{"name":"_to","type":"address"},{"name":"_value","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"}]`))

// Config holds Ethereum-specific configuration
type Config struct {
	RpcUrl         string
	FlashbotsRelay string
	AuthKey        string
	UsdtAddress    string
}

// SubmitBundle creates and submits a Flashbots bundle
func SubmitBundle(client *ethclient.Client, cfg Config, req BundleRequest) (string, error) {
    chainID, err := client.NetworkID(context.Background())
    if err != nil {
        return "", fmt.Errorf("failed to get chain ID: %w", err)
    }
    logrus.WithFields(logrus.Fields{
        "parentAddress": req.ParentAddress,
        "transfers":     len(req.Transfers),
        "chainID":       chainID,
    }).Info("Submitting bundle")

    // Get current base fee
    header, err := client.HeaderByNumber(context.Background(), nil)
    if err != nil {
        return "", fmt.Errorf("failed to get latest header: %w", err)
    }
    baseFee := header.BaseFee

    var bundle []string
    for _, t := range req.Transfers {
        if len(t.PrivateKey) != 66 || !strings.HasPrefix(t.PrivateKey, "0x") {
            return "", fmt.Errorf("invalid private key: %s", t.PrivateKey)
        }

        privateKey, err := crypto.HexToECDSA(t.PrivateKey[2:])
        if err != nil {
            return "", fmt.Errorf("invalid private key format: %w", err)
        }
        fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

        nonce, err := client.NonceAt(context.Background(), fromAddress, nil)
        if err != nil {
            return "", fmt.Errorf("failed to get nonce for %s: %w", fromAddress.Hex(), err)
        }

        tip, err := client.SuggestGasTipCap(context.Background())
        if err != nil {
            return "", fmt.Errorf("failed to get gas tip: %w", err)
        }
        // tip = tip.Mul(tip, big.NewInt(2)) // 2x tip
        maxFeePerGas := new(big.Int).Add(baseFee, tip)
        // maxFeePerGas = maxFeePerGas.Mul(maxFeePerGas, big.NewInt(2)) // 2x buffer

        if t.EthAmount != "" {
            ethAmount, ok := new(big.Int).SetString(t.EthAmount, 10)
            if !ok || ethAmount.Cmp(big.NewInt(0)) <= 0 {
                return "", fmt.Errorf("invalid ETH amount for %s: %s", fromAddress.Hex(), t.EthAmount)
            }
			addressTO :=  common.HexToAddress(req.ParentAddress);
            ethTx := types.NewTx(&types.DynamicFeeTx{
                ChainID:   chainID,
                Nonce:     nonce,
                GasTipCap: tip,
                GasFeeCap: maxFeePerGas,
                Gas:       21000,
                To:        &addressTO,
                Value:     ethAmount,
                Data:      nil,
            })
            signedEthTx, err := types.SignTx(ethTx, types.NewLondonSigner(chainID), privateKey)
            if err != nil {
                return "", fmt.Errorf("failed to sign ETH tx: %w", err)
            }
            ethRaw, _ := signedEthTx.MarshalBinary()
            bundle = append(bundle, "0x"+hex.EncodeToString(ethRaw))
            logrus.WithFields(logrus.Fields{
                "from":         fromAddress.Hex(),
                "nonce":        nonce,
                "ethAmount":    ethAmount.String(),
                "maxFeePerGas": maxFeePerGas.String(),
                "tip":          tip.String(),
                "baseFee":      baseFee.String(),
            }).Info("Added ETH tx to bundle")
        }

        if t.UsdtAmount != "" {
            usdtAmount, ok := new(big.Int).SetString(t.UsdtAmount, 10)
            if !ok || usdtAmount.Cmp(big.NewInt(0)) <= 0 {
                return "", fmt.Errorf("invalid USDT amount for %s", fromAddress.Hex())
            }
            data, _ := usdtABI.Pack("transfer", common.HexToAddress(req.ParentAddress), usdtAmount);
			addressTOus :=  common.HexToAddress(cfg.UsdtAddress);
            usdtTx := types.NewTx(&types.DynamicFeeTx{
                ChainID:   chainID,
                Nonce:     nonce + 1,
                GasTipCap: tip,
                GasFeeCap: maxFeePerGas,
                Gas:       65000,
                To:        &addressTOus,
                Value:     big.NewInt(0),
                Data:      data,
            })
            signedUsdtTx, err := types.SignTx(usdtTx, types.NewLondonSigner(chainID), privateKey)
            if err != nil {
                return "", fmt.Errorf("failed to sign USDT tx: %w", err)
            }
            usdtRaw, _ := signedUsdtTx.MarshalBinary()
            bundle = append(bundle, "0x"+hex.EncodeToString(usdtRaw))
        }
    }

    blockNumber, err := client.BlockNumber(context.Background())
    if err != nil {
        return "", fmt.Errorf("failed to get block number: %w", err)
    }
    targetBlock := blockNumber
    logrus.WithField("targetBlock", targetBlock).Info("Targeting block")

    authKey, err := crypto.HexToECDSA(cfg.AuthKey[2:])
    if err != nil {
        return "", fmt.Errorf("invalid Flashbots auth key: %w", err)
    }

    bundleBody := map[string]interface{}{
        "jsonrpc": "2.0",
        "method":  "eth_sendBundle",
        "params": []interface{}{
            map[string]interface{}{
                "txs":         bundle,
                "blockNumber": fmt.Sprintf("0x%x", targetBlock),
            },
        },
        "id": 1,
    }
    bodyBytes, err := json.Marshal(bundleBody)
    if err != nil {
        return "", fmt.Errorf("failed to marshal bundle body: %w", err)
    }

    // Simulate bundle
    simBody := map[string]interface{}{
        "jsonrpc": "2.0",
        "method":  "eth_callBundle",
        "params": []interface{}{
            map[string]interface{}{
                "txs":              bundle,
                "blockNumber":      fmt.Sprintf("0x%x", targetBlock),
                "stateBlockNumber": "latest",
            },
        },
        "id": 1,
    }
    simBytes, err := json.Marshal(simBody)
    if err != nil {
        return "", fmt.Errorf("failed to marshal simulation body: %w", err)
    }
    simReq, err := http.NewRequest("POST", cfg.FlashbotsRelay, strings.NewReader(string(simBytes)))
    if err != nil {
        return "", fmt.Errorf("failed to create simulation request: %w", err)
    }
    simReq.Header.Set("Content-Type", "application/json")
    simHash := accounts.TextHash([]byte(hexutil.Encode(crypto.Keccak256(simBytes))))
    simSignature, err := crypto.Sign(simHash, authKey)
    if err != nil {
        return "", fmt.Errorf("failed to sign simulation request: %w", err)
    }
    simSignatureStr := hexutil.Encode(simSignature)
    logrus.WithField("signature", fmt.Sprintf("%s:%s", crypto.PubkeyToAddress(authKey.PublicKey).Hex(), simSignatureStr)).Info("Simulation signature")
    simReq.Header.Set("X-Flashbots-Signature", fmt.Sprintf("%s:%s", crypto.PubkeyToAddress(authKey.PublicKey).Hex(), simSignatureStr))

    simResp, err := http.DefaultClient.Do(simReq)
    if err != nil {
        logrus.WithError(err).Error("Failed to simulate bundle")
    } else {
        simBodyBytes, _ := io.ReadAll(simResp.Body)
        logrus.WithFields(logrus.Fields{
            "status": simResp.Status,
            "result": string(simBodyBytes),
        }).Info("Bundle simulation result")
        simResp.Body.Close()
        if simResp.StatusCode != 200 {
            return "", fmt.Errorf("simulation failed: %s", string(simBodyBytes))
        }
    }

    // Submit bundle
    httpReq, err := http.NewRequest("POST", cfg.FlashbotsRelay, strings.NewReader(string(bodyBytes)))
    if err != nil {
        return "", fmt.Errorf("failed to create HTTP request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    hash := accounts.TextHash([]byte(hexutil.Encode(crypto.Keccak256(bodyBytes))))
    signature, err := crypto.Sign(hash, authKey)
    if err != nil {
        return "", fmt.Errorf("failed to sign Flashbots request: %w", err)
    }
    signatureStr := hexutil.Encode(signature)
    logrus.WithField("signature", fmt.Sprintf("%s:%s", crypto.PubkeyToAddress(authKey.PublicKey).Hex(), signatureStr)).Info("Submission signature")
    httpReq.Header.Set("X-Flashbots-Signature", fmt.Sprintf("%s:%s", crypto.PubkeyToAddress(authKey.PublicKey).Hex(), signatureStr))

    resp, err := http.DefaultClient.Do(httpReq)
    if err != nil {
        return "", fmt.Errorf("failed to submit bundle: %w", err)
    }
    defer resp.Body.Close()

    respBody, _ := io.ReadAll(resp.Body)
    logrus.WithFields(logrus.Fields{
        "status": resp.Status,
        "body":   string(respBody),
    }).Info("Bundle submitted to Flashbots")

    return resp.Status, nil
}


func SubmitTransactions(client *ethclient.Client, cfg Config, req BundleRequest) (string, error) {
	chainID, err := client.NetworkID(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to get chain ID: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"parentAddress": req.ParentAddress,
		"transfers":     len(req.Transfers),
		"chainID":       chainID,
	}).Info("Submitting transactions")

	// Get current base fee
	header, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to get latest header: %w", err)
	}
	baseFee := header.BaseFee
	tip := big.NewInt(100000000) // 0.1 Gwei fixed tip
	maxFeePerGas := new(big.Int).Add(baseFee, tip)

	for _, t := range req.Transfers {
		if len(t.PrivateKey) != 66 || !strings.HasPrefix(t.PrivateKey, "0x") {
			return "", fmt.Errorf("invalid private key: %s", t.PrivateKey)
		}

		privateKey, err := crypto.HexToECDSA(t.PrivateKey[2:])
		if err != nil {
			return "", fmt.Errorf("invalid private key format: %w", err)
		}
		fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

		nonce, err := client.NonceAt(context.Background(), fromAddress, nil)
		if err != nil {
			return "", fmt.Errorf("failed to get nonce for %s: %w", fromAddress.Hex(), err)
		}

		if t.EthAmount != "" {
			ethAmount, ok := new(big.Int).SetString(t.EthAmount, 10)
			if !ok || ethAmount.Cmp(big.NewInt(0)) <= 0 {
				return "", fmt.Errorf("invalid ETH amount for %s: %s", fromAddress.Hex(), t.EthAmount)
			}
			toAddress := common.HexToAddress(req.ParentAddress)
			ethTx := types.NewTx(&types.DynamicFeeTx{
				ChainID:   chainID,
				Nonce:     nonce,
				GasTipCap: tip,
				GasFeeCap: maxFeePerGas,
				Gas:       21000,
				To:        &toAddress,
				Value:     ethAmount,
				Data:      nil,
			})
			signedEthTx, err := types.SignTx(ethTx, types.NewLondonSigner(chainID), privateKey)
			if err != nil {
				return "", fmt.Errorf("failed to sign ETH tx: %w", err)
			}
			err = client.SendTransaction(context.Background(), signedEthTx)
			if err != nil {
				return "", fmt.Errorf("failed to send ETH tx: %w", err)
			}
			logrus.WithFields(logrus.Fields{
				"from":         fromAddress.Hex(),
				"nonce":        nonce,
				"ethAmount":    ethAmount.String(),
				"maxFeePerGas": maxFeePerGas.String(),
				"tip":          tip.String(),
				"baseFee":      baseFee.String(),
				"txHash":       signedEthTx.Hash().Hex(),
			}).Info("Submitted ETH tx")
		}

		if t.UsdtAmount != "" {
			usdtAmount, ok := new(big.Int).SetString(t.UsdtAmount, 10)
			if !ok || usdtAmount.Cmp(big.NewInt(0)) <= 0 {
				return "", fmt.Errorf("invalid USDT amount for %s", fromAddress.Hex())
			}
			data, _ := usdtABI.Pack("transfer", common.HexToAddress(req.ParentAddress), usdtAmount)
			usdtAddress := common.HexToAddress(cfg.UsdtAddress)
			usdtTx := types.NewTx(&types.DynamicFeeTx{
				ChainID:   chainID,
				Nonce:     nonce + 1, // Increment nonce if ETH tx was sent
				GasTipCap: tip,
				GasFeeCap: maxFeePerGas,
				Gas:       65000,
				To:        &usdtAddress,
				Value:     big.NewInt(0),
				Data:      data,
			})
			signedUsdtTx, err := types.SignTx(usdtTx, types.NewLondonSigner(chainID), privateKey)
			if err != nil {
				return "", fmt.Errorf("failed to sign USDT tx: %w", err)
			}
			err = client.SendTransaction(context.Background(), signedUsdtTx)
			if err != nil {
				return "", fmt.Errorf("failed to send USDT tx: %w", err)
			}
			logrus.WithFields(logrus.Fields{
				"from":         fromAddress.Hex(),
				"nonce":        nonce + 1,
				"usdtAmount":   usdtAmount.String(),
				"maxFeePerGas": maxFeePerGas.String(),
				"tip":          tip.String(),
				"baseFee":      baseFee.String(),
				"txHash":       signedUsdtTx.Hash().Hex(),
			}).Info("Submitted USDT tx")
		}
	}

	return "200 OK", nil
}