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

package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"

	"github.com/ItalyPaleAle/prvt/crypto"
	"github.com/ItalyPaleAle/prvt/fs/fsutils"
	"github.com/ItalyPaleAle/prvt/infofile"
	"github.com/ItalyPaleAle/prvt/utils"

	"github.com/Azure/azure-pipeline-go/pipeline"
	"github.com/Azure/azure-storage-blob-go/azblob"
)

// Register the fs
func init() {
	t := reflect.TypeOf((*AzureStorage)(nil)).Elem()
	fsTypes["azure"] = t
	fsTypes["azureblob"] = t
}

// AzureStorage stores files on Azure Blob Storage
type AzureStorage struct {
	fsBase

	storageAccountName string
	storageContainer   string
	storagePipeline    pipeline.Pipeline
	storageURL         string
	cache              *fsutils.MetadataCache
	mux                sync.Mutex
}

func (f *AzureStorage) OptionsList() *FsOptionsList {
	return &FsOptionsList{
		Label: "Azure Storage",
		Required: []FsOption{
			{Name: "storageAccount", Type: "string", Label: "Storage account name"},
			{Name: "accessKey", Type: "string", Label: "Storage account key", Private: true},
			{Name: "container", Type: "string", Label: "Container name"},
		},
		Optional: []FsOption{
			{Name: "endpointSuffix", Type: "string", Label: "Azure Storage endpoint suffix", Description: `Default: "core.windows.net" (Azure Cloud); use "core.chinacloudapi.cn" for Azure China, "core.cloudapi.de" for Azure Germany, "core.usgovcloudapi.net" for Azure Government`, Default: "core.windows.net"},
			{Name: "customEndpoint", Type: "string", Label: "Custom endpoint", Description: "For Azure Stack and other custom endpoints; endpoint suffix is ignored when this is set"},
			{Name: "tls", Type: "bool", Label: "Enable TLS", Default: "1"},
		},
	}
}

func (f *AzureStorage) InitWithOptionsMap(opts map[string]string, cache *fsutils.MetadataCache) error {
	// Required keys: "container", "storageAccount", "accessKey"
	// Optional keys: "tls", "endpointSuffix", "customEndpoint"

	// Load from the environment whatever we can (will be used as fallback
	f.loadEnvVars(opts)

	// Cache
	f.cache = cache

	// Container
	if opts["container"] == "" {
		return errors.New("option 'container' is not defined")
	}
	f.storageContainer = opts["container"]

	// Storage account name and key
	if opts["storageAccount"] == "" || opts["accessKey"] == "" {
		return errors.New("options 'storageAccount' and/or 'accessKey' are not defined")
	}
	f.storageAccountName = opts["storageAccount"]

	// Check if we're using TLS
	protocol := "https"
	tlsStr := strings.ToLower(opts["tls"])
	if tlsStr == "0" || tlsStr == "n" || tlsStr == "no" || tlsStr == "false" {
		protocol = "http"
	}

	// Check if need to use a custom storage endpoint (e.g. for Azurite)
	if opts["customEndpoint"] != "" {
		f.storageURL = fmt.Sprintf("%s://%s/%s/%s", protocol, opts["customEndpoint"], f.storageAccountName, f.storageContainer)
	} else {
		// Storage account endpoint suffix to support Azure China, Azure Germany, Azure Gov, or Azure Stack
		endpointSuffix := "core.windows.net"
		if opts["endpointSuffix"] != "" {
			endpointSuffix = opts["endpointSuffix"]
		}

		// Storage endpoint
		f.storageURL = fmt.Sprintf("%s://%s.blob.%s/%s", protocol, f.storageAccountName, endpointSuffix, f.storageContainer)
	}

	// Authenticate with Azure Storage
	credential, err := azblob.NewSharedKeyCredential(f.storageAccountName, opts["accessKey"])
	if err != nil {
		return err
	}
	f.storagePipeline = azblob.NewPipeline(credential, azblob.PipelineOptions{
		Retry: azblob.RetryOptions{
			MaxTries: 3,
		},
	})

	return nil
}

func (f *AzureStorage) loadEnvVars(opts map[string]string) {
	if opts["container"] == "" {
		opts["container"] = os.Getenv("AZURE_STORAGE_CONTAINER")
	}
	if opts["storageAccount"] == "" {
		opts["storageAccount"] = os.Getenv("AZURE_STORAGE_ACCOUNT")
	}
	if opts["accessKey"] == "" {
		opts["accessKey"] = os.Getenv("AZURE_STORAGE_ACCESS_KEY")
	}
	if opts["tls"] == "" {
		opts["tls"] = os.Getenv("AZURE_STORAGE_TLS")
	}
	if opts["endpointSuffix"] == "" {
		opts["endpointSuffix"] = os.Getenv("AZURE_STORAGE_ENDPOINT_SUFFIX")
	}
	if opts["customEndpoint"] == "" {
		opts["customEndpoint"] = os.Getenv("AZURE_STORAGE_CUSTOM_ENDPOINT")
	}
}

func (f *AzureStorage) InitWithConnectionString(connection string, cache *fsutils.MetadataCache) error {
	opts := make(map[string]string)

	// Ensure the connection string is valid and extract the parts
	// connection must start with "azureblob:" or "azure:"
	// Then it must contain the storage account container, and optionally the storage account name and access key
	parts := strings.Split(connection, ":")
	if len(parts) < 2 {
		return errors.New("invalid connection string")
	}
	opts["container"] = parts[1]

	// Check if we have the storage account name and access key
	if len(parts) >= 4 {
		opts["storageAccount"] = parts[2]
		opts["accessKey"] = parts[3]
	}

	// Init the object from the opts dictionary
	return f.InitWithOptionsMap(opts, cache)
}

func (f *AzureStorage) FSName() string {
	return "azure"
}

func (f *AzureStorage) AccountName() string {
	return f.storageAccountName + "/" + f.storageContainer
}

func (f *AzureStorage) RawGet(ctx context.Context, name string, out io.Writer, start int64, count int64) (found bool, tag interface{}, err error) {
	// Create the blob URL
	var blockBlobURL azblob.BlockBlobURL
	blockBlobURL, err = f.blobUrl(name)
	if err != nil {
		return
	}

	// Download the file or chunk
	if count < 1 {
		count = azblob.CountToEnd
	}
	resp, err := blockBlobURL.Download(ctx, start, count, azblob.BlobAccessConditions{}, false)
	if err != nil {
		if stgErr, ok := err.(azblob.StorageError); !ok {
			err = fmt.Errorf("network error while downloading the file: %s", err.Error())
		} else {
			// Blob not found
			if stgErr.Response().StatusCode == http.StatusNotFound {
				found = false
				err = nil
				return
			}
			err = fmt.Errorf("Azure Storage error while downloading the file: %s", stgErr.Response().Status)
		}
		return
	}
	body := resp.Body(azblob.RetryReaderOptions{
		MaxRetryRequests: 3,
	})
	defer body.Close()

	found = true

	// Check if the file exists but it's empty
	if resp.ContentLength() == 0 {
		found = false
		return
	}

	// Copy the response to the out stream
	_, err = io.Copy(out, body)
	if err != nil {
		return
	}

	// Get the ETag
	tagObj := resp.ETag()
	tag = &tagObj

	return
}

func (f *AzureStorage) RawSet(ctx context.Context, name string, in io.Reader, tag interface{}) (tagOut interface{}, err error) {
	// Create the blob URL
	var blockBlobURL azblob.BlockBlobURL
	blockBlobURL, err = f.blobUrl(name)
	if err != nil {
		return
	}

	// Get the blob access conditions
	accessConditions := f.blobAccessConditions(tag)

	// Upload the blob
	resp, err := azblob.UploadStreamToBlockBlob(ctx, in, blockBlobURL, azblob.UploadStreamToBlockBlobOptions{
		BufferSize:       3 * 1024 * 1024,
		MaxBuffers:       2,
		AccessConditions: accessConditions,
	})
	if err != nil {
		if stgErr, ok := err.(azblob.StorageError); !ok {
			return nil, fmt.Errorf("network error while uploading the file: %s", err.Error())
		} else {
			return nil, fmt.Errorf("Azure Storage error failed while uploading the file: %s", stgErr.Response().Status)
		}
	}

	// Get the ETag
	tagObj := resp.ETag()
	tagOut = &tagObj

	return tagOut, nil
}

func (f *AzureStorage) GetInfoFile() (info *infofile.InfoFile, err error) {
	// Create the blob URL
	var blockBlobURL azblob.BlockBlobURL
	blockBlobURL, err = f.blobUrl("_info.json")
	if err != nil {
		return
	}

	// Download the file
	resp, err := blockBlobURL.Download(context.Background(), 0, azblob.CountToEnd, azblob.BlobAccessConditions{}, false)
	if err != nil {
		if stgErr, ok := err.(azblob.StorageError); !ok {
			err = fmt.Errorf("network error while downloading the file: %s", err.Error())
		} else {
			// Blob not found
			if stgErr.Response().StatusCode == http.StatusNotFound {
				err = nil
				return
			}
			err = fmt.Errorf("Azure Storage error while downloading the file: %s", stgErr.Response().Status)
		}
		return
	}
	body := resp.Body(azblob.RetryReaderOptions{
		MaxRetryRequests: 3,
	})
	defer body.Close()

	// Read the entire file
	data, err := ioutil.ReadAll(body)
	if err != nil || len(data) == 0 {
		return
	}

	// Parse the JSON data
	info = &infofile.InfoFile{}
	if err = json.Unmarshal(data, info); err != nil {
		info = nil
		return
	}

	// Validate the content
	if err = info.Validate(); err != nil {
		info = nil
		return
	}

	// Set the data path
	f.dataPath = info.DataPath

	return
}

func (f *AzureStorage) SetInfoFile(info *infofile.InfoFile) (err error) {
	// Encode the content as JSON
	data, err := json.Marshal(info)
	if err != nil {
		return
	}

	// Create the blob URL
	var blockBlobURL azblob.BlockBlobURL
	blockBlobURL, err = f.blobUrl("_info.json")
	if err != nil {
		return
	}

	// Upload
	_, err = azblob.UploadBufferToBlockBlob(context.Background(), data, blockBlobURL, azblob.UploadToBlockBlobOptions{})
	if err != nil {
		if stgErr, ok := err.(azblob.StorageError); !ok {
			return fmt.Errorf("network error while uploading the file: %s", err.Error())
		} else {
			return fmt.Errorf("Azure Storage error failed while uploading the file: %s", stgErr.Response().Status)
		}
	}

	return
}

func (f *AzureStorage) Get(ctx context.Context, name string, out io.Writer, metadataCb crypto.MetadataCb) (found bool, tag interface{}, err error) {
	// Create the blob URL
	var blockBlobURL azblob.BlockBlobURL
	blockBlobURL, err = f.blobUrl(name)
	if err != nil {
		return
	}

	// Download the file
	resp, err := blockBlobURL.Download(ctx, 0, azblob.CountToEnd, azblob.BlobAccessConditions{}, false)
	if err != nil {
		if stgErr, ok := err.(azblob.StorageError); !ok {
			err = fmt.Errorf("network error while downloading the file: %s", err.Error())
		} else {
			// Blob not found
			if stgErr.Response().StatusCode == http.StatusNotFound {
				found = false
				err = nil
				return
			}
			err = fmt.Errorf("Azure Storage error while downloading the file: %s", stgErr.Response().Status)
		}
		return
	}
	body := resp.Body(azblob.RetryReaderOptions{
		MaxRetryRequests: 3,
	})
	defer body.Close()

	// Check if the file exists but it's empty
	if resp.ContentLength() == 0 {
		found = false
		return
	}

	found = true

	// Decrypt the data
	var metadataLength int32
	var metadata *crypto.Metadata
	headerVersion, headerLength, wrappedKey, err := crypto.DecryptFile(ctx, out, body, f.masterKey, func(md *crypto.Metadata, sz int32) bool {
		metadata = md
		metadataLength = sz
		if metadataCb != nil {
			metadataCb(md, sz)
		}
		return true
	})
	// Ignore ErrMetadataOnly so the metadata is still added to cache
	if err != nil && err != crypto.ErrMetadataOnly {
		return
	}

	// Store the metadata in cache
	// Adding a lock here to prevent the case when adding this key causes the eviction of another one that's in use
	f.mux.Lock()
	f.cache.Add(name, headerVersion, headerLength, wrappedKey, metadataLength, metadata)
	f.mux.Unlock()

	// Get the ETag
	tagObj := resp.ETag()
	tag = &tagObj

	return
}

func (f *AzureStorage) GetWithRange(ctx context.Context, name string, out io.Writer, rng *fsutils.RequestRange, metadataCb crypto.MetadataCb) (found bool, tag interface{}, err error) {
	// Create the blob URL
	var blockBlobURL azblob.BlockBlobURL
	blockBlobURL, err = f.blobUrl(name)
	if err != nil {
		return
	}

	var resp *azblob.DownloadResponse

	// Look up the file's metadata in the cache
	f.mux.Lock()
	headerVersion, headerLength, wrappedKey, metadataLength, metadata := f.cache.Get(name)
	if headerVersion == 0 || headerLength < 1 || wrappedKey == nil || len(wrappedKey) < 1 {
		// Need to request the metadata and cache it
		// For that, we need to request the header and the first package, which are at most 64kb + (32+256) bytes
		var length int64 = 64*1024 + 32 + 256
		innerCtx, cancel := context.WithCancel(ctx)
		resp, err = blockBlobURL.Download(innerCtx, 0, length, azblob.BlobAccessConditions{}, false)
		if err != nil {
			f.mux.Unlock()
			cancel()
			if stgErr, ok := err.(azblob.StorageError); !ok {
				err = fmt.Errorf("network error while downloading the file: %s", err.Error())
			} else {
				// Blob not found
				if stgErr.Response().StatusCode == http.StatusNotFound {
					found = false
					err = nil
					return
				}
				err = fmt.Errorf("Azure Storage error while downloading the file: %s", stgErr.Response().Status)
			}
			return
		}
		body := resp.Body(azblob.RetryReaderOptions{
			MaxRetryRequests: 3,
		})
		defer body.Close()

		// Check if the file exists but it's empty
		if resp.ContentLength() == 0 {
			f.mux.Unlock()
			cancel()
			found = false
			return
		}

		// Decrypt the data
		headerVersion, headerLength, wrappedKey, err = crypto.DecryptFile(innerCtx, nil, body, f.masterKey, func(md *crypto.Metadata, sz int32) bool {
			metadata = md
			metadataLength = sz
			cancel()
			return false
		})
		if err != nil && err != crypto.ErrMetadataOnly {
			f.mux.Unlock()
			cancel()
			return
		}

		// Store the metadata in cache
		f.cache.Add(name, headerVersion, headerLength, wrappedKey, metadataLength, metadata)
	}
	f.mux.Unlock()

	// Add the offsets to the range object and set the file size (it's guaranteed it's set, or we wouldn't have a range request)
	rng.HeaderOffset = int64(headerLength)
	rng.MetadataOffset = int64(metadataLength)
	rng.SetFileSize(metadata.Size)

	// Send the metadata to the callback
	if metadataCb != nil {
		metadataCb(metadata, metadataLength)
	}

	// Request the actual ranges that we need
	resp, err = blockBlobURL.Download(ctx, rng.StartBytes(), rng.LengthBytes(), azblob.BlobAccessConditions{}, false)
	if err != nil {
		if stgErr, ok := err.(azblob.StorageError); !ok {
			err = fmt.Errorf("network error while downloading the file: %s", err.Error())
		} else {
			// Blob not found
			if stgErr.Response().StatusCode == http.StatusNotFound {
				found = false
				err = nil
				return
			}
			err = fmt.Errorf("Azure Storage error while downloading the file: %s", stgErr.Response().Status)
		}
		return
	}
	body := resp.Body(azblob.RetryReaderOptions{
		MaxRetryRequests: 3,
	})
	defer body.Close()

	found = true

	// Check if the file exists but it's empty
	if resp.ContentLength() == 0 {
		found = false
		return
	}

	// Decrypt the data
	err = crypto.DecryptPackages(ctx, out, body, headerVersion, wrappedKey, f.masterKey, rng.StartPackage(), uint32(rng.SkipBeginning()), rng.Length, nil)
	if err != nil {
		return
	}

	// Get the ETag
	tagObj := resp.ETag()
	tag = &tagObj

	return
}

func (f *AzureStorage) Set(ctx context.Context, name string, in io.Reader, tag interface{}, metadata *crypto.Metadata) (tagOut interface{}, err error) {
	// Create the blob URL
	var blockBlobURL azblob.BlockBlobURL
	blockBlobURL, err = f.blobUrl(name)
	if err != nil {
		return
	}

	// Encrypt the data and upload it
	pr, pw := io.Pipe()
	var innerErr error
	go func() {
		defer pw.Close()
		r := utils.ReaderFuncWithContext(ctx, in)
		innerErr = crypto.EncryptFile(pw, r, f.masterKey, metadata)
	}()

	// Get the blob access conditions
	accessConditions := f.blobAccessConditions(tag)

	// Upload the blob
	resp, err := azblob.UploadStreamToBlockBlob(ctx, pr, blockBlobURL, azblob.UploadStreamToBlockBlobOptions{
		BufferSize:       3 * 1024 * 1024,
		MaxBuffers:       2,
		AccessConditions: accessConditions,
	})
	if innerErr != nil {
		return nil, innerErr
	}
	if err != nil {
		if stgErr, ok := err.(azblob.StorageError); !ok {
			return nil, fmt.Errorf("network error while uploading the file: %s", err.Error())
		} else {
			return nil, fmt.Errorf("Azure Storage error failed while uploading the file: %s", stgErr.Response().Status)
		}
	}

	// Get the ETag
	tagObj := resp.ETag()
	tagOut = &tagObj

	return tagOut, nil
}

func (f *AzureStorage) Delete(ctx context.Context, name string, tag interface{}) (err error) {
	// Create the blob URL
	var blockBlobURL azblob.BlockBlobURL
	blockBlobURL, err = f.blobUrl(name)
	if err != nil {
		return
	}

	// If we have a tag (ETag), we will allow the operation to succeed only if the tag matches
	// If there's no ETag, then it will always be allowed
	var accessConditions azblob.BlobAccessConditions
	if tag != nil {
		// Operation can succeed only if the file hasn't been modified since we downloaded it
		accessConditions = azblob.BlobAccessConditions{
			ModifiedAccessConditions: azblob.ModifiedAccessConditions{
				IfMatch: *tag.(*azblob.ETag),
			},
		}
	}

	// Delete the blob
	_, err = blockBlobURL.Delete(ctx, azblob.DeleteSnapshotsOptionInclude, accessConditions)
	return
}

// Internal function that returns the URL object for a blob
func (f *AzureStorage) blobUrl(name string) (blockBlobURL azblob.BlockBlobURL, err error) {
	if name == "" {
		err = errors.New("name is empty")
		return
	}

	// If the file doesn't start with _, it lives in a sub-folder inside the data path
	folder := ""
	if name[0] != '_' {
		folder = f.dataPath + "/"
	}

	// Create the blob URL
	u, err := url.Parse(f.storageURL + "/" + folder + name)
	if err != nil {
		return
	}
	blockBlobURL = azblob.NewBlockBlobURL(*u, f.storagePipeline)

	return
}

// Internal function that returns the BlobAccessConditions object for upload operations, given a tag
func (f *AzureStorage) blobAccessConditions(tag interface{}) (accessConditions azblob.BlobAccessConditions) {
	// If we have a tag (ETag), we will allow the upload to succeed only if the tag matches
	// If there's no ETag, then the upload can succeed only if there's no file already

	// Access conditions for blob uploads: disallow the operation if the blob already exists
	// See: https://docs.microsoft.com/en-us/rest/api/storageservices/specifying-conditional-headers-for-blob-service-operations#Subheading1
	if tag == nil {
		// Uploads can succeed only if there's no blob at that path yet
		accessConditions = azblob.BlobAccessConditions{
			ModifiedAccessConditions: azblob.ModifiedAccessConditions{
				IfNoneMatch: "*",
			},
		}
	} else {
		// Uploads can succeed only if the file hasn't been modified since we downloaded it
		accessConditions = azblob.BlobAccessConditions{
			ModifiedAccessConditions: azblob.ModifiedAccessConditions{
				IfMatch: *tag.(*azblob.ETag),
			},
		}
	}

	return
}
