package plugin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo"
	"github.com/stretchr/testify/assert"
)

func TestStatic(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(echo.GET, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("*")
	s := &Static{
		Root: "../_fixture",
	}
	s.Init()

	// File found
	c.SetParamValues("/images/walle.png")
	h := s.Process(echo.NotFoundHandler)
	if assert.NoError(t, h(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, rec.Header().Get(echo.HeaderContentLength), "219885")
	}

	// File not found
	c.SetParamValues("/none")
	rec.Body.Reset()
	h = s.Process(echo.NotFoundHandler)
	he := h(c).(*echo.HTTPError)
	assert.Equal(t, http.StatusNotFound, he.Code)

	// HTML5
	c.SetParamValues("/random")
	rec.Body.Reset()
	s.HTML5 = true
	h = s.Process(echo.NotFoundHandler)
	if assert.NoError(t, h(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Armor")
	}

	// Browse
	c.SetParamValues("/")
	rec.Body.Reset()
	s.Browse = true
	h = s.Process(echo.NotFoundHandler)
	if assert.NoError(t, h(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "images")
	}
}
