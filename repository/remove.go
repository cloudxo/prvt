/*
Copyright © 2020 Alessandro Segala (@ItalyPaleAle)

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/

package repository

import (
	"fmt"

	"github.com/ItalyPaleAle/prvt/index"
)

// RemovePath removes a path by its prefix, and reports each element removed in the res channel
func (repo *Repository) RemovePath(path string, res chan<- PathResultMessage) {
	// Remove from the index and get the list of objects to delete
	objects, paths, err := index.Instance.DeleteFile(path)
	if err != nil {
		res <- PathResultMessage{
			Path:   path,
			Status: RepositoryStatusInternalError,
			Err:    fmt.Errorf("Error while removing path from index: %s", err),
		}
		return
	}
	if objects == nil || len(objects) < 1 {
		res <- PathResultMessage{
			Path:   path,
			Status: RepositoryStatusNotFound,
		}
		return
	}

	// Delete the files
	for i := range objects {
		err = repo.Store.Delete(objects[i], nil)
		if err != nil {
			res <- PathResultMessage{
				Path:   paths[i],
				Status: RepositoryStatusInternalError,
				Err:    fmt.Errorf("Error while removing object from store: %s", err),
			}
			continue
		}

		res <- PathResultMessage{
			Path:   paths[i],
			Status: RepositoryStatusOK,
		}
	}

	return
}