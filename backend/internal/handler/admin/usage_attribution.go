package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/usageattribution"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) Attribution(c *gin.Context) {
	usageattribution.Report(c, h.usageService, true, false)
}
func (h *UsageHandler) AttributionExport(c *gin.Context) {
	usageattribution.Report(c, h.usageService, true, true)
}
func (h *UsageHandler) RenameAttribution(c *gin.Context) {
	usageattribution.Rename(c, h.usageService, true)
}
func (h *UsageHandler) AttributionOptions(c *gin.Context) {
	usageattribution.Options(c, h.usageService, true)
}
