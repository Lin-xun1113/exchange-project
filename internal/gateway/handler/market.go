package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	"github.com/linxun2025/exchange-project/pkg/response"
)

type levelView struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
	Orders   int32  `json:"orders"`
}

func levelsJSON(levels []*matchingpb.PriceLevel) []levelView {
	out := make([]levelView, len(levels))
	for i, level := range levels {
		out[i] = levelView{Price: level.GetPrice(), Quantity: level.GetQuantity(), Orders: level.GetOrderCount()}
	}
	return out
}

func (h *Handler) OrderBook(c *gin.Context) {
	market, ok := h.canonicalSymbol(c, c.Param("symbol"))
	if !ok {
		return
	}
	const defaultDepth = 20
	depth := int32(defaultDepth)
	if value := c.Query("depth"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil || parsed < 0 || parsed > 200 {
			response.BadRequest(c, "depth must be between 0 and 200")
			return
		}
		if parsed != 0 {
			depth = int32(parsed)
		}
	}
	result, err := h.cl.Matching.GetOrderBook(c.Request.Context(), &matchingpb.GetOrderBookRequest{Symbol: market, Depth: depth})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"symbol": result.GetSymbol(), "bids": levelsJSON(result.GetBids()), "asks": levelsJSON(result.GetAsks()), "lsn": result.GetLsn(),
	})
}
