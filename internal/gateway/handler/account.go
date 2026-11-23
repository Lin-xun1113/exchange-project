package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/pkg/response"
)

type balanceView struct {
	Asset     string `json:"asset"`
	Available string `json:"available"`
	Frozen    string `json:"frozen"`
}

func balanceJSON(b *userpb.Balance) balanceView {
	return balanceView{Asset: b.GetAsset(), Available: b.GetAvailable(), Frozen: b.GetFrozen()}
}

func (h *Handler) Balances(c *gin.Context) {
	result, err := h.cl.User.GetBalances(c.Request.Context(), &userpb.GetBalancesRequest{UserId: middleware.GetUserID(c)})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	balances := make([]balanceView, 0, len(result.GetBalances()))
	for _, b := range result.GetBalances() {
		balances = append(balances, balanceJSON(b))
	}
	response.Success(c, balances)
}

func (h *Handler) Deposit(c *gin.Context) {
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var req struct {
		Asset  string `json:"asset" binding:"required"`
		Amount string `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	uid := middleware.GetUserID(c)
	result, err := h.cl.User.Deposit(c.Request.Context(), &userpb.DepositRequest{
		UserId: uid,
		Asset:  req.Asset,
		Amount: req.Amount,
		RefId:  strconv.FormatInt(uid, 10) + ":" + key,
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	response.Success(c, balanceJSON(result.GetBalance()))
}
