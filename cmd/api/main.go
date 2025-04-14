package main

import (
	"database/sql"
	"flashbot-api/eth"
	"flashbot-api/api"
	"flashbot-api/config"
	"os"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

func main() {
	// Load configuration
	cfg, err := config.Load("config.yaml")
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}

	// Set up logging
	logrus.SetFormatter(&logrus.JSONFormatter{})
	if cfg.Environment == "testnet" {
		logrus.SetLevel(logrus.DebugLevel)
	}

	db, err := eth.InitDB(cfg.MySQL)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize database")
	}
	defer db.Close()

	logrus.WithFields(logrus.Fields{
		"rpc_url":      cfg.RpcUrl,
		"environment":  cfg.Environment,
		"port":         cfg.Port,
	}).Info("Starting Flashbots API")

	// Connect to Ethereum client
	client, err := ethclient.Dial(cfg.RpcUrl)
	if err != nil {
		logrus.Fatalf("Failed to connect to Ethereum node: %v", err)
	}

	// Initialize router
	r := gin.Default()

	// Create handler
	ethCfg := eth.Config{
		RpcUrl:         cfg.RpcUrl,
		FlashbotsRelay: cfg.FlashbotsRelay,
		AuthKey:        cfg.AuthKey,
		UsdtAddress:    cfg.UsdtAddress,
	}
	handler := api.NewHandler(client, ethCfg)

	// Register endpoints
	r.POST("/bundle", handler.SubmitBundle)
	r.POST("/transaction", handler.SubmitTransaction)
	r.GET("/health", handler.HealthCheck)

	// Start server
	if err := r.Run(":" + cfg.Port); err != nil {
		logrus.Fatalf("Failed to start server: %v", err)
	}
}