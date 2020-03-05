/*
Copyright © 2019 Alessandro Segala (@ItalyPaleAle)

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, version 3 of the License.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"e2e/crypto"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

// FileHandler is the handler for GET /file/:fileId, which returns a (decrypted) file
func (s *Server) FileHandler(c *gin.Context) {
	// Get the fileId
	fileId := c.Param("fileId")
	if fileId == "" {
		c.AbortWithError(http.StatusBadRequest, errors.New("empty fileId"))
		return
	}

	// Ensure fileId is a UUID
	fileIdUUID, err := uuid.FromString(fileId)
	if err != nil || fileIdUUID.Version() != 4 {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	// Load and decrypt the file, then pipe it to the response writer
	found, _, err := s.Store.Get(fileId, c.Writer, func(header *crypto.Header) {
		// Send headers before the data is sent
		if header.ContentType != "" {
			c.Header("Content-Type", header.ContentType)
		} else {
			c.Header("Content-Type", "application/octet-stream")
		}
		if header.Size > 0 {
			c.Header("Content-Length", strconv.FormatInt(header.Size, 10))
		}
		contentDisposition := "inline"
		if header.Name != "" {
			fileName := strings.ReplaceAll(header.Name, "\"", "")
			contentDisposition += "; filename=\"" + fileName + "\""
		}
		c.Header("Content-Disposition", contentDisposition)
	})
	if !found {
		c.AbortWithError(http.StatusNotFound, errors.New("file does not exist"))
		return
	}
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
}