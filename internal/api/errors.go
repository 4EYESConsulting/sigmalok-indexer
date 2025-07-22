package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// HTTPError represents a standardized API error response
type HTTPError struct {
	Code    int    `json:"code"    example:"400"`
	Message string `json:"message" example:"status bad request"`
}

// NewError creates and sends a new error response
func NewError(ctx *gin.Context, status int, err error) {
	er := HTTPError{
		Code:    status,
		Message: err.Error(),
	}
	ctx.JSON(status, er)
}

var (
	ErrInvalidRequest = fmt.Errorf("invalid request")
	ErrNotFound       = fmt.Errorf("resource not found")
	ErrServerError    = fmt.Errorf("internal server error")
	ErrLimitExceeded  = fmt.Errorf(
		"requested limit exceeds maximum allowed value",
	)
)

func BadRequest(ctx *gin.Context, err error) {
	NewError(ctx, http.StatusBadRequest, err)
}

func NotFound(ctx *gin.Context) {
	NewError(ctx, http.StatusNotFound, ErrNotFound)
}

func ServerError(ctx *gin.Context, err error) {
	NewError(
		ctx,
		http.StatusInternalServerError,
		fmt.Errorf("%w: %v", ErrServerError, err),
	)
}

func LimitExceeded(ctx *gin.Context, maxLimit int) {
	NewError(
		ctx,
		http.StatusBadRequest,
		fmt.Errorf("%w: maximum allowed is %d", ErrLimitExceeded, maxLimit),
	)
}
