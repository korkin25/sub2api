package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/usageattribution"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) RegisterAttribution(c *gin.Context) {
	usageattribution.Register(c, h.usageService)
}
func (h *UsageHandler) Attribution(c *gin.Context) {
	usageattribution.Report(c, h.usageService, false, false)
}
func (h *UsageHandler) AttributionExport(c *gin.Context) {
	usageattribution.Report(c, h.usageService, false, true)
}
func (h *UsageHandler) RenameAttribution(c *gin.Context) {
	usageattribution.Rename(c, h.usageService, false)
}
func (h *UsageHandler) AttributionOptions(c *gin.Context) {
	usageattribution.Options(c, h.usageService, false)
}
