package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Body struct {
	Code int         `json:"code"`
	Data interface{} `json:"data"`
	Msg  string      `json:"msg"`
}

func Success(c *gin.Context, data interface{}) {
	JSON(c, http.StatusOK, data, "")
}

func SuccessWithMsg(c *gin.Context, data interface{}, msg string) {
	JSON(c, http.StatusOK, data, msg)
}

func Error(c *gin.Context, status int, msg string) {
	JSON(c, status, gin.H{}, msg)
}

func JSON(c *gin.Context, status int, data interface{}, msg string) {
	if data == nil {
		data = gin.H{}
	}
	c.JSON(status, Body{
		Code: status,
		Data: data,
		Msg:  msg,
	})
}
