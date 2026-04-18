package handler

import (
	"net/http"

	"saras-tutor/config"

	"github.com/gin-gonic/gin"
)

// ExpertsHandler serves GET /api/v1/experts.
// Returns every model category with its candidate models sorted by priority.
// The frontend uses this to render per-category model pickers so the user
// can opt to retry a finished task with an alternative model.
func ExpertsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"categories": config.ListByCategory(),
	})
}
