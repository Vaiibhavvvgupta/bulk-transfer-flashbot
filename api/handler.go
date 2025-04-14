package api

import (
	"net/http"
	"strings"

	"flashbot-api/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Handler manages API endpoints
type Handler struct {
	client *ethclient.Client
	cfg    eth.Config
}

// NewHandler creates a new Handler
func NewHandler(client *ethclient.Client, cfg eth.Config) *Handler {
	return &Handler{
		client: client,
		cfg:    cfg,
	}
}

// HealthCheck returns a simple status to verify the server is running
func (h *Handler) HealthCheck(c *gin.Context) {
	logrus.Info("Health check requested")
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "Server is running",
		"rpc_url": h.cfg.RpcUrl,
	})
}

// SubmitBundle handles the /bundle endpoint
func (h *Handler) SubmitBundle(c *gin.Context) {
	var req eth.BundleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logrus.WithError(err).Error("Failed to bind JSON")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !common.IsHexAddress(req.ParentAddress) {
		logrus.Error("Invalid parent address")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parent address"})
		return
	}

	if len(req.Transfers) == 0 {
		logrus.Error("No transfers provided")
		c.JSON(http.StatusBadRequest, gin.H{"error": "No transfers provided"})
		return
	}

	status, err := eth.SubmitBundle(h.client, h.cfg, req)
	if err != nil {
		logrus.WithError(err).Error("Failed to submit bundle")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Check Flashbots response status
	if !strings.Contains(status, "200") {
		logrus.WithField("status", status).Warn("Bundle rejected by Flashbots")
		c.JSON(http.StatusBadGateway, gin.H{
			"status":  status,
			"message": "Bundle rejected by Flashbots",
		})
		return
	}

	logrus.Info("Bundle submitted successfully")
	c.JSON(http.StatusOK, gin.H{
		"status":  status,
		"message": "Bundle submitted successfully",
	})
}
func (h *Handler) SubmitTransaction(c *gin.Context) {
	var req eth.BundleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logrus.WithError(err).Error("Failed to parse request body")
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "400 Bad Request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate parentAddress
	if req.ParentAddress == "" || len(req.ParentAddress) != 42 || !strings.HasPrefix(req.ParentAddress, "0x") {
		logrus.WithField("parentAddress", req.ParentAddress).Error("Invalid parent address")
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "400 Bad Request",
			"message": "Invalid parentAddress",
		})
		return
	}

	// Validate transfers
	if len(req.Transfers) == 0 {
		logrus.Error("No transfers provided")
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "400 Bad Request",
			"message": "At least one transfer is required",
		})
		return
	}

	// Submit transactions
	status, err := eth.SubmitTransactions(h.client, h.cfg, req)
	if err != nil {
		logrus.WithError(err).Error("Failed to submit transactions")
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "500 Internal Server Error",
			"message": err.Error(),
		})
		return
	}

	logrus.Info("Transactions submitted successfully")
	c.JSON(http.StatusOK, gin.H{
		"status":  status,
		"message": "Transactions submitted successfully",
	})
}