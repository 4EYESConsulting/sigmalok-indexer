package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"

	_ "4EYESConsulting/sigmalok-indexer/docs"
	"4EYESConsulting/sigmalok-indexer/internal/config"
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/logging"
	"4EYESConsulting/sigmalok-indexer/internal/types"

	"github.com/anthdm/hollywood/actor"
	scalargo "github.com/bdpiprava/scalar-go"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sigmaspace-io/ergo-lib-go"
)

//	@title			SigmaLok Indexer API
//	@version	    1.0.0

//	@license.name	Apache 2.0
//	@license.url	http://www.apache.org/licenses/LICENSE-2.0.html

//	@host		localhost:8080
//	@BasePath	/

//	@tag.name				contracts
//	@tag.description		Contract Queries

//	@tag.name				statistics
//	@tag.description		Global statistics and metrics about contracts and locked assets

//	@tag.name				system
//	@tag.description		System status and health monitoring endpoints

//	@schemes	http https

var scalarHTML []byte
var scalarHTMLGenerationErr error

func generateScalarDocs() {

	specDir := "./docs"

	baseFileName := "swagger.json"

	htmlContent, err := scalargo.NewV2(
		scalargo.WithSpecDir(specDir),
		scalargo.WithBaseFileName(baseFileName),
		scalargo.WithTheme(scalargo.ThemeBluePlanet),
		scalargo.WithMetaDataOpts(
			scalargo.WithTitle("SigmaLok Indexer API"),
		),
		scalargo.WithLayout(scalargo.LayoutClassic),
	)

	if err != nil {
		scalarHTMLGenerationErr = fmt.Errorf(
			"failed to generate Scalar documentation: %w",
			err,
		)
		logging.GetLogger().
			Error("Failed to generate Scalar documentation", "error", scalarHTMLGenerationErr)
		scalarHTML = nil
		return
	}
	scalarHTML = []byte(htmlContent)
	scalarHTMLGenerationErr = nil
	logging.GetLogger().Info("Scalar API documentation generated successfully.")
}

func Start(
	cfg *config.Config,
	db *database.Database,
	indexerPID *actor.PID,
	engine *actor.Engine,
) (*http.Server, error) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	router.Use(func(c *gin.Context) {
		c.Set("db", db)
		c.Set("cfg", cfg)
		c.Next()
	})

	router.Use(func(c *gin.Context) {
		resp := engine.Request(indexerPID, types.GetStatus{}, 5*time.Second)
		if resp == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "failed to get indexer status: timeout",
			})
			return
		}

		result, err := resp.Result()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("failed to get indexer status: %v", err),
			})
			return
		}

		status, ok := result.(types.Status)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "failed to get indexer status: invalid response type",
			})
			return
		}

		c.Set("indexer_status", IndexerStatus{
			LastProcessedHeight: status.LastProcessedHeight,
			TipHeight:           status.TipHeight,
			PendingDownstream:   status.PendingDownstream,
			ReadersAlive:        status.ReadersAlive,
		})
		c.Next()
	})

	logger := logging.GetLogger()
	router.Use(ginzap.GinzapWithConfig(logger.Desugar(), &ginzap.Config{
		TimeFormat: time.RFC3339,
		UTC:        true,
	}))
	router.Use(ginzap.RecoveryWithZap(logger.Desugar(), true))

	// // WebSocket endpoint
	// router.GET("/ws", func(c *gin.Context) {
	// 	wsManager.HandleWebSocket(c)
	// })

	// Health check endpoint
	router.GET("/healthcheck", handleHealthcheck)

	// Contract endpoints
	router.GET("/contracts/:singleton", handleGetContract)
	router.GET("/contracts/creator/:creator", handleGetContractsByCreator)
	router.GET(
		"/contracts/benefactor/:benefactor",
		handleGetContractsByBenefactor,
	)
	router.GET("/contracts/deadline/:deadline", handleGetContractsByDeadline)
	router.POST("/contracts/keys", handleGetContractsByKeys)
	router.POST("/contracts/designates", handleGetContractsByDesignates)
	router.GET("/contracts", handleGetAllContracts)

	// Statistics endpoints
	router.GET("/statistics", handleGetStatistics)
	router.GET("/statistics/claimable-erg/:height", handleGetClaimableErg)

	// Setup metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	if cfg.Server.EnableDebug {
		debugGroup := router.Group("/debug/pprof")
		{
			debugGroup.GET("/", gin.WrapF(pprof.Index))
			debugGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
			debugGroup.GET("/profile", gin.WrapF(pprof.Profile))
			debugGroup.POST("/symbol", gin.WrapF(pprof.Symbol))
			debugGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
			debugGroup.GET("/trace", gin.WrapF(pprof.Trace))
			debugGroup.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
			debugGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
			debugGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
			debugGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
			debugGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
			debugGroup.GET(
				"/threadcreate",
				gin.WrapH(pprof.Handler("threadcreate")),
			)
		}
	}

	// Generate and setup API docs
	generateScalarDocs()
	router.GET("/docs", func(c *gin.Context) {
		if scalarHTMLGenerationErr != nil {
			logger.Error(
				"API documentation unavailable",
				"error",
				scalarHTMLGenerationErr,
			)
			c.String(
				http.StatusNotFound,
				"API documentation is currently unavailable.",
			)
			return
		}
		if scalarHTML == nil {
			c.String(
				http.StatusNotFound,
				"API documentation is currently unavailable (not generated).",
			)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", scalarHTML)
	})

	serverAddr := fmt.Sprintf("%s:%d",
		cfg.Server.ListenAddress,
		cfg.Server.ListenPort,
	)

	logger.Info("Starting API server", "address", serverAddr)
	logger.Info("API Documentation available at /docs")
	logger.Info("Metrics available at /metrics")
	if cfg.Server.EnableDebug {
		logger.Info("Debug endpoints available at /debug/pprof/*")
	}

	server := &http.Server{
		Addr:    serverAddr,
		Handler: router,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			logger.Error("API server failed", "error", err)
		}
	}()

	return server, nil
}

// handleHealthcheck godoc
//
//	@Summary		Health Check
//	@Description	Returns 200 only when all readers are alive, lastProcessedHeight+cfg.AllowedLag ≥ tipHeight, and pendingDownstream < cfg.AllowedBuffer
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	ReadinessResponse	"Service is ready"
//	@Success		503	{object}	ReadinessResponse	"Service is not ready"
//	@Router			/healthcheck [get]
func handleHealthcheck(c *gin.Context) {
	cfg := c.MustGet("cfg").(*config.Config)
	status := c.MustGet("indexer_status").(IndexerStatus)

	// Check if we're within allowed lag
	withinLag := status.LastProcessedHeight+cfg.Indexer.AllowedLag >= status.TipHeight

	// Check if pending downstream buffer is within limits
	withinBuffer := status.PendingDownstream <= cfg.Indexer.AllowedBuffer

	// All conditions must be met
	ready := status.ReadersAlive && withinLag && withinBuffer

	resp := ReadinessResponse{Ready: ready}
	if !ready {
		resp.Message = buildNotReadyMessage(status, cfg)
		c.JSON(http.StatusServiceUnavailable, resp)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func buildNotReadyMessage(status IndexerStatus, cfg *config.Config) string {
	var reasons []string

	if !status.ReadersAlive {
		reasons = append(reasons, "Kafka readers are not healthy")
	}

	if status.LastProcessedHeight+cfg.Indexer.AllowedLag < status.TipHeight {
		reasons = append(reasons, fmt.Sprintf(
			"Chain sync lag too high (current: %d, tip: %d, allowed lag: %d)",
			status.LastProcessedHeight,
			status.TipHeight,
			cfg.Indexer.AllowedLag,
		))
	}

	if status.PendingDownstream >= cfg.Indexer.AllowedBuffer {
		reasons = append(reasons, fmt.Sprintf(
			"Too many pending events (current: %d, max allowed: %d)",
			status.PendingDownstream,
			cfg.Indexer.AllowedBuffer,
		))
	}

	return fmt.Sprintf("Not ready: %s", strings.Join(reasons, "; "))
}

// handleGetContract godoc
//
//	@Summary		Get Contract by Singleton
//	@Description	Retrieves a contract and its associated data by singleton identifier
//	@Tags			contracts
//	@Accept			json
//	@Produce		json
//	@Param			singleton	path		string						true	"Singleton identifier of the contract"
//	@Success		200			{object}	ContractResponse			"Contract details successfully retrieved"
//	@Failure		404			{object}	map[string]string			"Contract not found"
//	@Failure		500			{object}	map[string]string			"Internal server error"
//	@Router			/contracts/{singleton} [get]
func handleGetContract(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	singleton := c.Param("singleton")

	if !Is32ByteHex(singleton) {
		BadRequest(
			c,
			fmt.Errorf(
				"%w: singleton must be a 32-byte hex string",
				ErrInvalidRequest,
			),
		)
		return
	}

	contract, err := db.GetContractBySingleton(singleton)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get contract: %w", err))
		return
	}

	if contract == nil {
		NotFound(c)
		return
	}

	c.JSON(http.StatusOK, contract)
}

// handleGetContractsByCreator godoc
//
//	@Summary		Get Contracts by Creator
//	@Description	Retrieves all contracts created by a specific address with pagination support
//	@Tags			contracts
//	@Accept			json
//	@Produce		json
//	@Param			creator	path		string						true	"Creator's Ergo address"
//	@Param			limit	query		int							false	"Number of contracts to return (default 100)"
//	@Param			offset	query		int							false	"Number of contracts to skip (default 0)"
//	@Success		200		{array}		ContractResponse			"List of contracts successfully retrieved"
//	@Failure		500		{object}	map[string]string			"Internal server error"
//	@Router			/contracts/creator/{creator} [get]
func handleGetContractsByCreator(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	creator := c.Param("creator")

	if !IsValidErgoAddress(creator) {
		BadRequest(
			c,
			fmt.Errorf("%w: invalid creator address", ErrInvalidRequest),
		)
		return
	}

	limit, offset := getPaginationParams(c)
	if c.IsAborted() {
		return
	}

	contracts, err := db.GetContractsByCreatorAddress(creator, limit, offset)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get contracts: %w", err))
		return
	}

	c.JSON(http.StatusOK, contracts)
}

// handleGetContractsByBenefactor godoc
//
//	@Summary		Get Contracts by Benefactor
//	@Description	Retrieves all contracts where the specified address is the benefactor with pagination support
//	@Tags			contracts
//	@Accept			json
//	@Produce		json
//	@Param			benefactor	path		string						true	"Benefactor's Ergo address"
//	@Param			limit		query		int							false	"Number of contracts to return (default 100)"
//	@Param			offset		query		int							false	"Number of contracts to skip (default 0)"
//	@Success		200			{array}		ContractResponse			"List of contracts successfully retrieved"
//	@Failure		500			{object}	map[string]string			"Internal server error"
//	@Router			/contracts/benefactor/{benefactor} [get]
func handleGetContractsByBenefactor(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	benefactor := c.Param("benefactor")

	if !IsValidErgoAddress(benefactor) {
		BadRequest(
			c,
			fmt.Errorf("%w: invalid benefactor address", ErrInvalidRequest),
		)
		return
	}

	limit, offset := getPaginationParams(c)
	if c.IsAborted() {
		return
	}

	contracts, err := db.GetContractsByBenefactorAddress(
		benefactor,
		limit,
		offset,
	)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get contracts: %w", err))
		return
	}

	c.JSON(http.StatusOK, contracts)
}

// handleGetContractsByDeadline godoc
//
//	@Summary		Get Contracts by Deadline
//	@Description	Retrieves all contracts with deadline before or after the specified block height
//	@Tags			contracts
//	@Accept			json
//	@Produce		json
//	@Param			deadline	path		uint64						true	"Block height deadline"
//	@Param			after		query		bool						false	"If true, returns contracts after deadline instead (default false)"
//	@Param			limit		query		int							false	"Number of contracts to return (default 100)"
//	@Param			offset		query		int							false	"Number of contracts to skip (default 0)"
//	@Success		200			{array}		ContractResponse			"List of contracts successfully retrieved"
//	@Failure		500			{object}	map[string]string			"Internal server error"
//	@Router			/contracts/deadline/{deadline} [get]
func handleGetContractsByDeadline(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	deadline := parseUint64(c.Param("deadline"))
	after := c.DefaultQuery("after", "false") == "true"
	limit, offset := getPaginationParams(c)
	if c.IsAborted() {
		return
	}

	var contracts []database.ContractResponse
	var err error
	if after {
		contracts, err = db.GetContractsAfterDeadline(deadline, limit, offset)
	} else {
		contracts, err = db.GetContractsBeforeOrOnDeadline(deadline, limit, offset)
	}
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get contracts: %w", err))
		return
	}

	c.JSON(http.StatusOK, contracts)
}

// handleGetContractsByKeys godoc
//
//	@Summary		Get Contracts by Keys
//	@Description	Retrieves all contracts that have any of the specified keys
//	@Tags			contracts
//	@Accept			json
//	@Produce		json
//	@Param			request	body		KeysRequest					true	"List of key IDs to search for"
//	@Param			limit	query		int							false	"Number of contracts to return (default 100)"
//	@Param			offset	query		int							false	"Number of contracts to skip (default 0)"
//	@Success		200		{array}		ContractResponse			"List of contracts successfully retrieved"
//	@Failure		400		{object}	map[string]string			"Invalid request body"
//	@Failure		500		{object}	map[string]string			"Internal server error"
//	@Router			/contracts/keys [post]
func handleGetContractsByKeys(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	var keys KeysRequest
	if err := c.ShouldBindJSON(&keys); err != nil {
		BadRequest(c, fmt.Errorf("%w: invalid request body", ErrInvalidRequest))
		return
	}

	if len(keys) == 0 {
		BadRequest(c, fmt.Errorf("%w: no keys provided", ErrInvalidRequest))
		return
	}

	// Validate each key
	for i, key := range keys {
		if !Is32ByteHex(key) {
			BadRequest(
				c,
				fmt.Errorf(
					"%w: key at index %d must be a 32-byte hex string",
					ErrInvalidRequest,
					i,
				),
			)
			return
		}
	}

	limit, offset := getPaginationParams(c)
	if c.IsAborted() {
		return
	}

	contracts, err := db.GetContractsByKeys(keys, limit, offset)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get contracts: %w", err))
		return
	}

	c.JSON(http.StatusOK, contracts)
}

// handleGetContractsByDesignates godoc
//
//	@Summary		Get Contracts by Designate Addresses
//	@Description	Retrieves all contracts that have any of the specified designate addresses
//	@Tags			contracts
//	@Accept			json
//	@Produce		json
//	@Param			request	body		DesignatesRequest			true	"List of designate addresses to search for"
//	@Param			limit	query		int							false	"Number of contracts to return (default 100)"
//	@Param			offset	query		int							false	"Number of contracts to skip (default 0)"
//	@Success		200		{array}		ContractResponse			"List of contracts successfully retrieved"
//	@Failure		400		{object}	map[string]string			"Invalid request body"
//	@Failure		500		{object}	map[string]string			"Internal server error"
//	@Router			/contracts/designates [post]
func handleGetContractsByDesignates(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	var addresses DesignatesRequest
	if err := c.ShouldBindJSON(&addresses); err != nil {
		BadRequest(c, fmt.Errorf("%w: invalid request body", ErrInvalidRequest))
		return
	}

	if len(addresses) == 0 {
		BadRequest(
			c,
			fmt.Errorf("%w: no addresses provided", ErrInvalidRequest),
		)
		return
	}

	for i, address := range addresses {
		if !IsValidErgoAddress(address) {
			BadRequest(
				c,
				fmt.Errorf(
					"%w: invalid designate address at index %d",
					ErrInvalidRequest,
					i,
				),
			)
			return
		}
	}

	limit, offset := getPaginationParams(c)
	if c.IsAborted() {
		return
	}

	contracts, err := db.GetContractsByDesignateAddresses(
		addresses,
		limit,
		offset,
	)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get contracts: %w", err))
		return
	}

	c.JSON(http.StatusOK, contracts)
}

// handleGetAllContracts godoc
//
//	@Summary		Get All Contracts
//	@Description	Retrieves all contracts with pagination support
//	@Tags			contracts
//	@Accept			json
//	@Produce		json
//	@Param			limit	query		int							false	"Number of contracts to return (default 100)"
//	@Param			offset	query		int							false	"Number of contracts to skip (default 0)"
//	@Success		200		{array}		ContractResponse			"List of contracts successfully retrieved"
//	@Failure		500		{object}	map[string]string			"Internal server error"
//	@Router			/contracts [get]
func handleGetAllContracts(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	limit, offset := getPaginationParams(c)
	if c.IsAborted() {
		return
	}

	contracts, err := db.GetAllContracts(limit, offset)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get contracts: %w", err))
		return
	}

	c.JSON(http.StatusOK, contracts)
}

// handleGetStatistics godoc
//
//	@Summary		Get Global Statistics
//	@Description	Retrieves global statistics about contracts and locked assets
//	@Tags			statistics
//	@Accept			json
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"Statistics successfully retrieved"
//	@Failure		500	{object}	map[string]string		"Internal server error"
//	@Router			/statistics [get]
func handleGetStatistics(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	status := c.MustGet("indexer_status").(IndexerStatus)

	totalContracts, err := db.GetTotalContractCount()
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get total contracts: %w", err))
		return
	}

	totalLockedErg, err := db.GetTotalLockedErg()
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get total locked ERG: %w", err))
		return
	}

	totalUniqueTokens, err := db.GetTotalUniqueTokenCount()
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get total unique tokens: %w", err))
		return
	}

	totalRedeems, err := db.GetTotalRedeemCount()
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get total redeems: %w", err))
		return
	}

	totalAvailableErg, err := db.GetTotalClaimableErgAfterHeight(
		status.TipHeight,
	)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get total available ERG: %w", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total_contracts":     totalContracts,
		"total_locked_erg":    totalLockedErg,
		"total_unique_tokens": totalUniqueTokens,
		"total_redeems":       totalRedeems,
		"total_available_erg": totalAvailableErg,
	})
}

// handleGetClaimableErg godoc
//
//	@Summary		Get Total Claimable ERG
//	@Description	Retrieves the total amount of ERG that will be claimable after a specific block height
//	@Tags			statistics
//	@Accept			json
//	@Produce		json
//	@Param			height	path		uint64				true	"Block height to check claimable ERG"
//	@Success		200		{object}	map[string]uint64	"Total claimable ERG successfully retrieved"
//	@Failure		500		{object}	map[string]string	"Internal server error"
//	@Router			/statistics/claimable-erg/{height} [get]
func handleGetClaimableErg(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	height := parseUint64(c.Param("height"))

	claimableErg, err := db.GetTotalClaimableErgAfterHeight(height)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get claimable ERG: %w", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{"claimable_erg": claimableErg})
}

// Request types for documentation

// KeysRequest represents a request containing a list of key IDs
//
//	@Description	Request body for searching contracts by keys
type KeysRequest []string

// DesignatesRequest represents a request containing a list of designate addresses
//
//	@Description	Request body for searching contracts by designate addresses
type DesignatesRequest []string

// RedeemType represents the type of redemption
// @Description Type of redemption (deadline_reached, oracle_redeem, or designate_redeem)
type RedeemType string

const (
	// RedeemTypeDeadlineReached indicates redemption after deadline
	RedeemTypeDeadlineReached RedeemType = "deadline_reached"
	// RedeemTypeOracleRedeem indicates redemption via oracle condition
	RedeemTypeOracleRedeem RedeemType = "oracle_redeem"
	// RedeemTypeDesignateRedeem indicates redemption by a designate
	RedeemTypeDesignateRedeem RedeemType = "designate_redeem"
)

// ContractResponse represents a smart contract with all its associated data
// @Description Contract response object containing all contract details and associated transactions
type ContractResponse struct {
	// Unique identifier of the contract
	Singleton string `json:"singleton"`
	// Block height where the contract was created
	BlockHeight uint64 `json:"block_height"`
	// Optional name of the contract
	Name *string `json:"name"`
	// Address of the contract creator
	Creator string `json:"creator"`
	// Address of the contract benefactor
	Benefactor string `json:"benefactor"`
	// Block height deadline for the contract
	Deadline uint64 `json:"deadline"`
	// Optional oracle NFT token ID
	OracleNFT *string `json:"oracle_nft"`
	// Optional oracle value to compare against
	OracleValue *uint64 `json:"oracle_value"`
	// Optional flag indicating if oracle comparison should be greater than
	OracleIsGreaterThan *bool `json:"oracle_is_greater_than"`
	// Transaction ID of the genesis transaction
	GenesisTx string `json:"genesis_tx"`
	// Initial ERG amount in the contract
	GenesisErg uint64 `json:"genesis_erg"`
	// Whether the genesis transaction is confirmed
	GenesisConfirmed bool `json:"genesis_confirmed"`
	// Hash of the fee parameters
	FeeHash string `json:"fee_hash"`
	// Amount of the fee
	FeeAmount int `json:"fee_amount"`
	// Optional key information
	Key *KeyInfo `json:"key"`
	// List of designate addresses and their block heights
	Designates []DesignateInfo `json:"designates"`
	// List of funding transactions
	FundingTxs []FundingTxInfo `json:"funding_txs"`
	// List of mint transactions
	MintTxs []MintTxInfo `json:"mint_txs"`
	// List of redeem transactions
	RedeemTxs []RedeemTxInfo `json:"redeem_txs"`
	// Current value of the contract including ERG and tokens
	Value ContractValue `json:"value"`
}

// KeyInfo represents key information for a contract
// @Description Key information including ID and amount
type KeyInfo struct {
	// Unique identifier of the key
	ID string `json:"id"`
	// Amount associated with the key
	Amount uint64 `json:"amount"`
}

// DesignateInfo represents a designate address entry
// @Description Designate address information with block height
type DesignateInfo struct {
	// Ergo address of the designate
	Address string `json:"address"`
	// Block height when the designate was added
	BlockHeight uint64 `json:"block_height"`
}

// FundingTxInfo represents a funding transaction
// @Description Funding transaction information
type FundingTxInfo struct {
	// Transaction ID
	Tx string `json:"tx"`
	// Change in ERG amount
	ErgDelta uint64 `json:"erg_delta"`
	// Block height of the transaction
	BlockHeight uint64 `json:"block_height"`
}

// MintTxInfo represents a mint transaction
// @Description Mint transaction information
type MintTxInfo struct {
	// Transaction ID
	Tx string `json:"tx"`
	// Block height of the transaction
	BlockHeight uint64 `json:"block_height"`
	// Whether the transaction is confirmed
	Confirmed bool `json:"confirmed"`
}

// RedeemTxInfo represents a redeem transaction
// @Description Redeem transaction information
type RedeemTxInfo struct {
	// Transaction ID
	Tx string `json:"tx"`
	// Address receiving the redeemed assets
	RecipientAddress string `json:"recipient_address"`
	// Type of redemption
	Type RedeemType `json:"type"`
	// Block height of the transaction
	BlockHeight uint64 `json:"block_height"`
	// Whether the transaction is confirmed
	Confirmed bool `json:"confirmed"`
}

// ContractValue represents the current value held by a contract
// @Description Current value of the contract including ERG and tokens
type ContractValue struct {
	// ERG value information
	ERG struct {
		// Confirmed ERG amount
		Confirmed uint64 `json:"confirmed"`
		// Unconfirmed ERG amount
		Unconfirmed uint64 `json:"unconfirmed"`
	} `json:"erg"`
	// Token value information
	Tokens struct {
		// Map of confirmed token balances
		Confirmed map[string]TokenBalance `json:"confirmed"`
		// Map of unconfirmed token balances
		Unconfirmed map[string]TokenBalance `json:"unconfirmed"`
	} `json:"tokens"`
}

// TokenBalance represents a token balance
// @Description Token balance information
type TokenBalance struct {
	// Token identifier
	TokenID string `json:"tokenId"`
	// Token amount
	Amount uint64 `json:"amount"`
}

// Helper functions
func getPaginationParams(c *gin.Context) (limit, offset int) {
	cfg := c.MustGet("cfg").(*config.Config)
	limit = parseIntWithDefault(c.Query("limit"), 100)
	offset = parseIntWithDefault(c.Query("offset"), 0)

	// Enforce max limit if configured
	if cfg.Server.MaxLimit > 0 && limit > cfg.Server.MaxLimit {
		LimitExceeded(c, cfg.Server.MaxLimit)
		c.Abort()
		return 0, 0
	}

	return limit, offset
}

func parseIntWithDefault(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

func parseUint64(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

type ReadinessResponse struct {
	Ready   bool   `json:"ready"`
	Message string `json:"message,omitempty"`
}

type IndexerStatus struct {
	LastProcessedHeight uint64
	TipHeight           uint64
	PendingDownstream   int
	ReadersAlive        bool
}

// Validation helpers
func IsHex(s string) bool {
	if len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func Is32ByteHex(s string) bool {
	return len(s) == 64 && IsHex(s)
}

func IsValidErgoAddress(address string) bool {
	_, err := ergo.NewAddress(address)
	return err == nil
}
