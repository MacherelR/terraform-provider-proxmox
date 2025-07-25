/*
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 */

package resource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/bpg/terraform-provider-proxmox/proxmox"
	"github.com/bpg/terraform-provider-proxmox/proxmox/api"
	"github.com/bpg/terraform-provider-proxmox/proxmox/version"
	"github.com/bpg/terraform-provider-proxmox/proxmoxtf"
	"github.com/bpg/terraform-provider-proxmox/proxmoxtf/resource/validators"
	"github.com/bpg/terraform-provider-proxmox/utils"
)

const (
	dvResourceVirtualEnvironmentFileSourceFileChanged  = false
	dvResourceVirtualEnvironmentFileSourceFileChecksum = ""
	dvResourceVirtualEnvironmentFileSourceFileFileName = ""
	dvResourceVirtualEnvironmentFileSourceFileInsecure = false
	dvResourceVirtualEnvironmentFileSourceFileMinTLS   = ""
	dvResourceVirtualEnvironmentFileOverwrite          = true
	dvResourceVirtualEnvironmentFileSourceRawResize    = 0
	dvResourceVirtualEnvironmentFileTimeoutUpload      = 1800

	mkResourceVirtualEnvironmentFileContentType          = "content_type"
	mkResourceVirtualEnvironmentFileDatastoreID          = "datastore_id"
	mkResourceVirtualEnvironmentFileFileModificationDate = "file_modification_date"
	mkResourceVirtualEnvironmentFileFileName             = "file_name"
	mkResourceVirtualEnvironmentFileFileMode             = "file_mode"
	mkResourceVirtualEnvironmentFileFileSize             = "file_size"
	mkResourceVirtualEnvironmentFileFileTag              = "file_tag"
	mkResourceVirtualEnvironmentFileNodeName             = "node_name"
	mkResourceVirtualEnvironmentFileOverwrite            = "overwrite"
	mkResourceVirtualEnvironmentFileSourceFile           = "source_file"
	mkResourceVirtualEnvironmentFileSourceFilePath       = "path"
	mkResourceVirtualEnvironmentFileSourceFileChanged    = "changed"
	mkResourceVirtualEnvironmentFileSourceFileChecksum   = "checksum"
	mkResourceVirtualEnvironmentFileSourceFileFileName   = "file_name"
	mkResourceVirtualEnvironmentFileSourceFileInsecure   = "insecure"
	mkResourceVirtualEnvironmentFileSourceFileMinTLS     = "min_tls"
	mkResourceVirtualEnvironmentFileSourceRaw            = "source_raw"
	mkResourceVirtualEnvironmentFileSourceRawData        = "data"
	mkResourceVirtualEnvironmentFileSourceRawFileName    = "file_name"
	mkResourceVirtualEnvironmentFileSourceRawResize      = "resize"
	mkResourceVirtualEnvironmentFileTimeoutUpload        = "timeout_upload"
)

// File returns a resource that manages files on a node.
func File() *schema.Resource {
	return &schema.Resource{
		Schema: map[string]*schema.Schema{
			mkResourceVirtualEnvironmentFileContentType: {
				Type:             schema.TypeString,
				Description:      "The content type",
				Optional:         true,
				ForceNew:         true,
				Computed:         true,
				ValidateDiagFunc: validators.ContentType(),
			},
			mkResourceVirtualEnvironmentFileDatastoreID: {
				Type:        schema.TypeString,
				Description: "The datastore id",
				Required:    true,
				ForceNew:    true,
			},
			mkResourceVirtualEnvironmentFileFileModificationDate: {
				Type:        schema.TypeString,
				Description: "The file modification date",
				Computed:    true,
				ForceNew:    true,
			},
			mkResourceVirtualEnvironmentFileFileName: {
				Type:        schema.TypeString,
				Description: "The file name",
				Computed:    true,
			},
			mkResourceVirtualEnvironmentFileFileMode: {
				Type: schema.TypeString,
				Description: `The file mode in octal format, e.g. "0700" or "600".` +
					`Note that the prefixes "0o" and "0x" are not supported!` +
					`Setting this attribute is also only allowed for "root@pam" authenticated user.`,
				Optional:         true,
				ValidateDiagFunc: validators.FileMode(),
				ForceNew:         true,
			},
			mkResourceVirtualEnvironmentFileFileSize: {
				Type:        schema.TypeInt,
				Description: "The file size in bytes",
				Computed:    true,
				ForceNew:    true,
			},
			mkResourceVirtualEnvironmentFileFileTag: {
				Type:        schema.TypeString,
				Description: "The file tag",
				Computed:    true,
				ForceNew:    true,
			},
			mkResourceVirtualEnvironmentFileNodeName: {
				Type:        schema.TypeString,
				Description: "The node name",
				Required:    true,
				ForceNew:    true,
			},
			mkResourceVirtualEnvironmentFileSourceFile: {
				Type:        schema.TypeList,
				Description: "The source file",
				Optional:    true,
				ForceNew:    true,
				DefaultFunc: func() (interface{}, error) {
					return make([]interface{}, 1), nil
				},
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						mkResourceVirtualEnvironmentFileSourceFilePath: {
							Type:        schema.TypeString,
							Description: "A path to a local file or a URL",
							Required:    true,
							ForceNew:    true,
						},
						mkResourceVirtualEnvironmentFileSourceFileChanged: {
							Type:        schema.TypeBool,
							Description: "Whether the source file has changed since the last run",
							Optional:    true,
							ForceNew:    true,
							Default:     dvResourceVirtualEnvironmentFileSourceFileChanged,
						},
						mkResourceVirtualEnvironmentFileSourceFileChecksum: {
							Type:        schema.TypeString,
							Description: "The SHA256 checksum of the source file",
							Optional:    true,
							ForceNew:    true,
							Default:     dvResourceVirtualEnvironmentFileSourceFileChecksum,
						},
						mkResourceVirtualEnvironmentFileSourceFileFileName: {
							Type:        schema.TypeString,
							Description: "The file name to use instead of the source file name",
							Optional:    true,
							ForceNew:    true,
							Default:     dvResourceVirtualEnvironmentFileSourceFileFileName,
						},
						mkResourceVirtualEnvironmentFileSourceFileInsecure: {
							Type:        schema.TypeBool,
							Description: "Whether to skip the TLS verification step for HTTPS sources",
							Optional:    true,
							ForceNew:    true,
							Default:     dvResourceVirtualEnvironmentFileSourceFileInsecure,
						},
						mkResourceVirtualEnvironmentFileSourceFileMinTLS: {
							Type: schema.TypeString,
							Description: "The minimum required TLS version for HTTPS sources." +
								"Supported values: `1.0|1.1|1.2|1.3`. Defaults to `1.3`.",
							Optional: true,
							ForceNew: true,
							Default:  dvResourceVirtualEnvironmentFileSourceFileMinTLS,
						},
					},
				},
				MaxItems: 1,
				MinItems: 0,
			},
			mkResourceVirtualEnvironmentFileSourceRaw: {
				Type:        schema.TypeList,
				Description: "The raw source",
				Optional:    true,
				ForceNew:    true,
				DefaultFunc: func() (interface{}, error) {
					return make([]interface{}, 1), nil
				},
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						mkResourceVirtualEnvironmentFileSourceRawData: {
							Type:        schema.TypeString,
							Description: "The raw data",
							Required:    true,
							ForceNew:    true,
						},
						mkResourceVirtualEnvironmentFileSourceRawFileName: {
							Type:        schema.TypeString,
							Description: "The file name",
							Required:    true,
							ForceNew:    true,
						},
						mkResourceVirtualEnvironmentFileSourceRawResize: {
							Type:        schema.TypeInt,
							Description: "The number of bytes to resize the file to",
							Optional:    true,
							ForceNew:    true,
							Default:     dvResourceVirtualEnvironmentFileSourceRawResize,
						},
					},
				},
				MaxItems: 1,
				MinItems: 0,
			},
			mkResourceVirtualEnvironmentFileTimeoutUpload: {
				Type:        schema.TypeInt,
				Description: "Timeout for uploading ISO/VSTMPL files in seconds",
				Optional:    true,
				Default:     dvResourceVirtualEnvironmentFileTimeoutUpload,
			},
			mkResourceVirtualEnvironmentFileOverwrite: {
				Type:        schema.TypeBool,
				Description: "Whether to overwrite the file if it already exists",
				Optional:    true,
				Default:     dvResourceVirtualEnvironmentFileOverwrite,
			},
		},
		CreateContext: fileCreate,
		ReadContext:   fileRead,
		DeleteContext: fileDelete,
		UpdateContext: fileUpdate,
		Importer: &schema.ResourceImporter{
			StateContext: func(_ context.Context, d *schema.ResourceData, _ interface{}) ([]*schema.ResourceData, error) {
				node, volID, err := fileParseImportID(d.Id())
				if err != nil {
					return nil, err
				}

				d.SetId(volID.String())

				err = d.Set(mkResourceVirtualEnvironmentFileNodeName, node)
				if err != nil {
					return nil, fmt.Errorf("failed setting 'node_name' in state during import: %w", err)
				}

				err = d.Set(mkResourceVirtualEnvironmentFileDatastoreID, volID.datastoreID)
				if err != nil {
					return nil, fmt.Errorf("failed setting 'datastore_id' in state during import: %w", err)
				}

				err = d.Set(mkResourceVirtualEnvironmentFileContentType, volID.contentType)
				if err != nil {
					return nil, fmt.Errorf("failed setting 'content_type' in state during import: %w", err)
				}

				return []*schema.ResourceData{d}, nil
			},
		},
	}
}

type fileVolumeID struct {
	datastoreID string
	contentType string
	fileName    string
}

func (v fileVolumeID) String() string {
	return fmt.Sprintf("%s:%s/%s", v.datastoreID, v.contentType, v.fileName)
}

// fileParseVolumeID parses a volume ID in the format datastore_id:content_type/file_name.
func fileParseVolumeID(id string) (fileVolumeID, error) {
	parts := strings.SplitN(id, ":", 2)

	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fileVolumeID{}, fmt.Errorf("unexpected format of ID (%s), expected datastore_id:content_type/file_name", id)
	}

	datastoreID := parts[0]

	parts = strings.SplitN(parts[1], "/", 2)

	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fileVolumeID{}, fmt.Errorf("unexpected format of ID (%s), expected datastore_id:content_type/file_name", id)
	}

	contentType := parts[0]
	fileName := parts[1]

	return fileVolumeID{
		datastoreID: datastoreID,
		contentType: contentType,
		fileName:    fileName,
	}, nil
}

// fileParseImportID parses an import ID in the format node/datastore_id:content_type/file_name.
func fileParseImportID(id string) (string, fileVolumeID, error) {
	parts := strings.SplitN(id, "/", 2)

	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fileVolumeID{},
			fmt.Errorf("unexpected format of ID (%s), expected node/datastore_id:content_type/file_name", id)
	}

	node := parts[0]

	volID, err := fileParseVolumeID(parts[1])
	if err != nil {
		return "", fileVolumeID{}, err
	}

	return node, volID, nil
}

func fileCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	uploadTimeout := d.Get(mkResourceVirtualEnvironmentFileTimeoutUpload).(int)
	fileMode := d.Get(mkResourceVirtualEnvironmentFileFileMode).(string)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(uploadTimeout)*time.Second)
	defer cancel()

	var diags diag.Diagnostics

	fileName, err := fileGetSourceFileName(d)
	diags = append(diags, diag.FromErr(err)...)

	if diags.HasError() {
		return diags
	}

	nodeName := d.Get(mkResourceVirtualEnvironmentFileNodeName).(string)
	datastoreID := d.Get(mkResourceVirtualEnvironmentFileDatastoreID).(string)

	config := m.(proxmoxtf.ProviderConfiguration)

	capi, err := config.GetClient()
	if err != nil {
		return diag.FromErr(err)
	}

	contentType, dg := fileGetContentType(ctx, d, capi)
	diags = append(diags, dg...)

	list, err := capi.Node(nodeName).Storage(datastoreID).ListDatastoreFiles(ctx)
	if err != nil {
		return diag.FromErr(err)
	}

	for _, file := range list {
		volumeID, e := fileParseVolumeID(file.VolumeID)
		if e != nil {
			tflog.Warn(ctx, "failed to parse volume ID", map[string]interface{}{
				"error": err,
			})

			continue
		}

		if volumeID.fileName == *fileName {
			if d.Get(mkResourceVirtualEnvironmentFileOverwrite).(bool) {
				diags = append(diags, diag.Diagnostic{
					Severity: diag.Warning,
					Summary:  fmt.Sprintf("the existing file %q has been overwritten by the resource", volumeID),
				})
			} else {
				return diag.Errorf("file %q already exists", volumeID)
			}
		}
	}

	sourceFile := d.Get(mkResourceVirtualEnvironmentFileSourceFile).([]interface{})
	sourceRaw := d.Get(mkResourceVirtualEnvironmentFileSourceRaw).([]interface{})

	sourceFilePathLocal := ""

	// Determine if both source_data and source_file is specified as this is not supported.
	if len(sourceFile) > 0 && len(sourceRaw) > 0 {
		diags = append(diags, diag.Errorf(
			"please specify \"%s.%s\" or \"%s\" - not both",
			mkResourceVirtualEnvironmentFileSourceFile,
			mkResourceVirtualEnvironmentFileSourceFilePath,
			mkResourceVirtualEnvironmentFileSourceRaw,
		)...)
	}

	if diags.HasError() {
		return diags
	}

	// Determine if we're dealing with raw file data or a reference to a file or URL.
	// In case of a URL, we must first download the file before proceeding.
	// This is due to lack of support for chunked transfers in the Proxmox VE API.
	if len(sourceFile) > 0 {
		sourceFileBlock := sourceFile[0].(map[string]interface{})
		sourceFilePath := sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFilePath].(string)
		sourceFileChecksum := sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFileChecksum].(string)
		sourceFileMinTLS := sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFileMinTLS].(string)
		sourceFileInsecure := sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFileInsecure].(bool)

		if fileIsURL(d) {
			tflog.Debug(ctx, "Downloading file from URL", map[string]interface{}{
				"url": sourceFilePath,
			})

			minTLSVersion, e := api.GetMinTLSVersion(sourceFileMinTLS)
			if e != nil {
				return diag.FromErr(e)
			}

			httpClient := http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						MinVersion:         minTLSVersion,
						InsecureSkipVerify: sourceFileInsecure,
					},
				},
			}

			res, err := httpClient.Get(sourceFilePath)
			if err != nil {
				return diag.FromErr(err)
			}

			defer utils.CloseOrLogError(ctx)(res.Body)

			tempDownloadedFile, err := os.CreateTemp(config.TempDir(), "download")
			if err != nil {
				return diag.FromErr(err)
			}

			tempDownloadedFileName := tempDownloadedFile.Name()
			defer func(name string) {
				err := os.Remove(name)
				if err != nil {
					tflog.Error(ctx, "Failed to remove temporary file", map[string]interface{}{
						"error": err,
						"file":  name,
					})
				}
			}(tempDownloadedFileName)

			_, err = io.Copy(tempDownloadedFile, res.Body)
			diags = append(diags, diag.FromErr(err)...)
			err = tempDownloadedFile.Close()
			diags = append(diags, diag.FromErr(err)...)

			if diags.HasError() {
				return diags
			}

			sourceFilePathLocal = tempDownloadedFileName
		} else {
			sourceFilePathLocal = sourceFilePath
		}

		// Calculate the checksum of the source file now that it's available locally.
		if sourceFileChecksum != "" {
			file, err := os.Open(sourceFilePathLocal)
			if err != nil {
				return diag.FromErr(err)
			}

			h := sha256.New()
			_, err = io.Copy(h, file)
			diags = append(diags, diag.FromErr(err)...)
			err = file.Close()
			diags = append(diags, diag.FromErr(err)...)
			if diags.HasError() {
				return diags
			}

			calculatedChecksum := fmt.Sprintf("%x", h.Sum(nil))
			tflog.Debug(ctx, "Calculated checksum", map[string]interface{}{
				"source": sourceFilePath,
				"sha256": calculatedChecksum,
			})

			if sourceFileChecksum != calculatedChecksum {
				return diag.Errorf(
					"the calculated SHA256 checksum \"%s\" does not match source checksum \"%s\"",
					calculatedChecksum,
					sourceFileChecksum,
				)
			}
		}
	}

	//nolint:nestif
	if len(sourceRaw) > 0 {
		sourceRawBlock := sourceRaw[0].(map[string]interface{})
		sourceRawData := sourceRawBlock[mkResourceVirtualEnvironmentFileSourceRawData].(string)
		sourceRawResize := sourceRawBlock[mkResourceVirtualEnvironmentFileSourceRawResize].(int)

		if sourceRawResize > 0 {
			if len(sourceRawData) <= sourceRawResize {
				sourceRawData = fmt.Sprintf(fmt.Sprintf("%%-%dv", sourceRawResize), sourceRawData)
			} else {
				return diag.Errorf("cannot resize %d bytes to %d bytes", len(sourceRawData), sourceRawResize)
			}
		}

		tempRawFile, e := os.CreateTemp(config.TempDir(), "raw")
		if e != nil {
			return diag.FromErr(err)
		}

		tempRawFileName := tempRawFile.Name()
		_, err = io.Copy(tempRawFile, bytes.NewBufferString(sourceRawData))
		diags = append(diags, diag.FromErr(err)...)
		err = tempRawFile.Close()
		diags = append(diags, diag.FromErr(err)...)
		if diags.HasError() {
			return diags
		}

		defer func(name string) {
			err := os.Remove(name)
			if err != nil {
				tflog.Error(ctx, "Failed to remove temporary file", map[string]interface{}{
					"error": err,
					"file":  name,
				})
			}
		}(tempRawFileName)

		sourceFilePathLocal = tempRawFileName
	}

	// Open the source file for reading in order to upload it.
	file, err := os.Open(sourceFilePathLocal)
	if err != nil {
		return diag.FromErr(err)
	}

	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			tflog.Error(ctx, "Failed to close file", map[string]interface{}{
				"error": err,
			})
		}
	}(file)

	request := &api.FileUploadRequest{
		ContentType: *contentType,
		FileName:    *fileName,
		File:        file,
		Mode:        fileMode,
	}

	switch *contentType {
	case "iso", "vztmpl", "import":
		_, err = capi.Node(nodeName).Storage(datastoreID).APIUpload(
			ctx, request, config.TempDir(),
		)
		if err != nil {
			diags = append(diags, diag.FromErr(err)...)
			return diags
		}
	default:
		// For all other content types, we need to upload the file to the node's
		// datastore using SFTP.
		datastore, err2 := capi.Storage().GetDatastore(ctx, datastoreID)
		if err2 != nil {
			return diag.Errorf("failed to get datastore: %s", err2)
		}

		if datastore.Path == nil || *datastore.Path == "" {
			return diag.Errorf("failed to determine the datastore path")
		}

		sort.Strings(datastore.Content)

		_, found := slices.BinarySearch(datastore.Content, *contentType)
		if !found {
			diags = append(diags, diag.Diagnostics{
				diag.Diagnostic{
					Severity: diag.Warning,
					Summary: fmt.Sprintf("the datastore %q does not support content type %q; supported content types are: %v",
						*datastore.Storage, *contentType, datastore.Content,
					),
				},
			}...)
		}

		// PVE expects backups to be located at the "dump" directory of the datastore.
		if *contentType == "backup" {
			request.ContentType = "dump"
		}

		err = capi.SSH().NodeStreamUpload(ctx, nodeName, *datastore.Path, request)
		if err != nil {
			diags = append(diags, diag.FromErr(err)...)
			return diags
		}

	}

	volID, di := fileGetVolumeID(ctx, d, capi)
	diags = append(diags, di...)
	if diags.HasError() {
		return diags
	}

	d.SetId(volID.String())

	diags = append(diags, fileRead(ctx, d, m)...)

	if d.Id() == "" {
		diags = append(diags, diag.Errorf("failed to read file from %q", volID.String())...)
	}

	return diags
}

func fileGetContentType(ctx context.Context, d *schema.ResourceData, c proxmox.Client) (*string, diag.Diagnostics) {
	contentType := d.Get(mkResourceVirtualEnvironmentFileContentType).(string)
	sourceFile := d.Get(mkResourceVirtualEnvironmentFileSourceFile).([]interface{})
	sourceRaw := d.Get(mkResourceVirtualEnvironmentFileSourceRaw).([]interface{})

	ver := version.MinimumProxmoxVersion
	if versionResp, err := c.Version().Version(ctx); err == nil {
		ver = versionResp.Version
	} else {
		tflog.Warn(ctx, fmt.Sprintf("failed to determine Proxmox VE version, assume %v", ver), map[string]interface{}{
			"error": err,
		})
	}

	sourceFilePath := ""

	if len(sourceFile) > 0 {
		sourceFileBlock := sourceFile[0].(map[string]interface{})
		sourceFilePath = sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFilePath].(string)
	} else if len(sourceRaw) > 0 {
		sourceRawBlock := sourceRaw[0].(map[string]interface{})
		sourceFilePath = sourceRawBlock[mkResourceVirtualEnvironmentFileSourceRawFileName].(string)
	} else {
		return nil, diag.Errorf(
			"missing argument \"%s.%s\" or \"%s\"",
			mkResourceVirtualEnvironmentFileSourceFile,
			mkResourceVirtualEnvironmentFileSourceFilePath,
			mkResourceVirtualEnvironmentFileSourceRaw,
		)
	}
	if contentType == "" {
		if strings.HasSuffix(sourceFilePath, ".tar.gz") ||
			strings.HasSuffix(sourceFilePath, ".tar.xz") {
			contentType = "vztmpl"
		} else if ver.SupportImportContentType() &&
			(strings.HasSuffix(sourceFilePath, ".qcow2") ||
				strings.HasSuffix(sourceFilePath, ".raw") ||
				strings.HasSuffix(sourceFilePath, ".vmdk")) {
			contentType = "import"
		} else {
			ext := strings.TrimLeft(strings.ToLower(filepath.Ext(sourceFilePath)), ".")

			switch ext {
			case "img", "iso":
				contentType = "iso"
			case "yaml", "yml":
				contentType = "snippets"
			}
		}

		if contentType == "" {
			return nil, diag.Errorf(
				"cannot determine the content type of source \"%s\" - Please manually define the \"%s\" argument",
				sourceFilePath,
				mkResourceVirtualEnvironmentFileContentType,
			)
		}
	}

	ctValidator := validators.ContentType()
	diags := ctValidator(contentType, cty.GetAttrPath(mkResourceVirtualEnvironmentFileContentType))

	return &contentType, diags
}

func fileGetSourceFileName(d *schema.ResourceData) (*string, error) {
	sourceFile := d.Get(mkResourceVirtualEnvironmentFileSourceFile).([]interface{})
	sourceRaw := d.Get(mkResourceVirtualEnvironmentFileSourceRaw).([]interface{})

	sourceFileFileName := ""
	sourceFilePath := ""

	if len(sourceFile) > 0 {
		sourceFileBlock := sourceFile[0].(map[string]interface{})
		sourceFileFileName = sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFileFileName].(string)
		sourceFilePath = sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFilePath].(string)
	} else if len(sourceRaw) > 0 {
		sourceRawBlock := sourceRaw[0].(map[string]interface{})
		sourceFileFileName = sourceRawBlock[mkResourceVirtualEnvironmentFileSourceRawFileName].(string)
	} else {
		return nil, fmt.Errorf(
			"missing argument \"%s.%s\"",
			mkResourceVirtualEnvironmentFileSourceFile,
			mkResourceVirtualEnvironmentFileSourceFilePath,
		)
	}

	if sourceFileFileName == "" {
		if fileIsURL(d) {
			downloadURL, err := url.ParseRequestURI(sourceFilePath)
			if err != nil {
				return nil, err
			}

			path := strings.Split(downloadURL.Path, "/")
			sourceFileFileName = path[len(path)-1]

			if sourceFileFileName == "" {
				return nil, fmt.Errorf(
					"failed to determine file name from the URL \"%s\"",
					sourceFilePath,
				)
			}
		} else {
			sourceFileFileName = filepath.Base(sourceFilePath)
		}
	}

	return &sourceFileFileName, nil
}

func fileGetVolumeID(ctx context.Context, d *schema.ResourceData, c proxmox.Client) (fileVolumeID, diag.Diagnostics) {
	fileName, err := fileGetSourceFileName(d)
	if err != nil {
		return fileVolumeID{}, diag.FromErr(err)
	}

	datastoreID := d.Get(mkResourceVirtualEnvironmentFileDatastoreID).(string)
	contentType, diags := fileGetContentType(ctx, d, c)

	return fileVolumeID{
		datastoreID: datastoreID,
		contentType: *contentType,
		fileName:    *fileName,
	}, diags
}

func fileIsURL(d *schema.ResourceData) bool {
	sourceFile := d.Get(mkResourceVirtualEnvironmentFileSourceFile).([]interface{})
	sourceFilePath := ""

	if len(sourceFile) > 0 {
		sourceFileBlock := sourceFile[0].(map[string]interface{})
		sourceFilePath = sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFilePath].(string)
	} else {
		return false
	}

	return strings.HasPrefix(sourceFilePath, "http://") ||
		strings.HasPrefix(sourceFilePath, "https://")
}

func fileRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	config := m.(proxmoxtf.ProviderConfiguration)
	capi, err := config.GetClient()
	if err != nil {
		return diag.FromErr(err)
	}

	datastoreID := d.Get(mkResourceVirtualEnvironmentFileDatastoreID).(string)
	nodeName := d.Get(mkResourceVirtualEnvironmentFileNodeName).(string)
	sourceFile := d.Get(mkResourceVirtualEnvironmentFileSourceFile).([]interface{})

	list, err := capi.Node(nodeName).Storage(datastoreID).ListDatastoreFiles(ctx)
	if err != nil {
		return diag.FromErr(err)
	}

	readFileAttrs := readFile
	if fileIsURL(d) {
		readFileAttrs = readURL(capi.API().HTTP())
	}

	var diags diag.Diagnostics

	found := false
	for _, v := range list {
		if v.VolumeID == d.Id() {
			found = true

			volID, err := fileParseVolumeID(v.VolumeID)
			diags = append(diags, diag.FromErr(err)...)

			err = d.Set(mkResourceVirtualEnvironmentFileFileName, volID.fileName)
			diags = append(diags, diag.FromErr(err)...)

			err = d.Set(mkResourceVirtualEnvironmentFileContentType, v.ContentType)
			diags = append(diags, diag.FromErr(err)...)

			if len(sourceFile) == 0 {
				continue
			}

			sourceFileBlock := sourceFile[0].(map[string]interface{})
			sourceFilePath := sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFilePath].(string)

			fileModificationDate, fileSize, fileTag, err := readFileAttrs(ctx, sourceFilePath)
			diags = append(diags, diag.FromErr(err)...)

			if fileModificationDate != "" || fileSize != 0 || fileTag != "" {
				// only when file from state exists
				err = d.Set(mkResourceVirtualEnvironmentFileFileModificationDate, fileModificationDate)
				diags = append(diags, diag.FromErr(err)...)
				err = d.Set(mkResourceVirtualEnvironmentFileFileSize, fileSize)
				diags = append(diags, diag.FromErr(err)...)
				err = d.Set(mkResourceVirtualEnvironmentFileFileTag, fileTag)
				diags = append(diags, diag.FromErr(err)...)
			}

			lastFileMD := d.Get(mkResourceVirtualEnvironmentFileFileModificationDate).(string)
			lastFileSize := int64(d.Get(mkResourceVirtualEnvironmentFileFileSize).(int))
			lastFileTag := d.Get(mkResourceVirtualEnvironmentFileFileTag).(string)

			// just to make the logic easier to read
			changed := false
			if lastFileMD != "" && lastFileSize != 0 && lastFileTag != "" {
				changed = lastFileMD != fileModificationDate || lastFileSize != fileSize || lastFileTag != fileTag
			}

			sourceFileBlock[mkResourceVirtualEnvironmentFileSourceFileChanged] = changed
			err = d.Set(mkResourceVirtualEnvironmentFileSourceFile, sourceFile)
			diags = append(diags, diag.FromErr(err)...)

			if diags.HasError() {
				return diags
			}
			return nil
		}
	}

	if !found {
		// an empty ID is used to signal that the resource does not exist when provider reads the state
		// back after creation, or on the state refresh.
		d.SetId("")
	}

	return nil
}

//nolint:nonamedreturns
func readFile(
	ctx context.Context,
	sourceFilePath string,
) (fileModificationDate string, fileSize int64, fileTag string, err error) {
	f, err := os.Open(sourceFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File does not exist, return zero values and no error
			return "", 0, "", nil
		}
		return
	}

	defer func(f *os.File) {
		e := f.Close()
		if e != nil {
			tflog.Error(ctx, "failed to close the file", map[string]interface{}{
				"error": e.Error(),
			})
		}
	}(f)

	fileInfo, err := f.Stat()
	if err != nil {
		return
	}

	fileModificationDate = fileInfo.ModTime().UTC().Format(time.RFC3339)
	fileSize = fileInfo.Size()
	fileTag = fmt.Sprintf("%x-%x", fileInfo.ModTime().UTC().Unix(), fileInfo.Size())

	return fileModificationDate, fileSize, fileTag, nil
}

func readURL(
	httClient *http.Client,
) func(
	ctx context.Context,
	sourceFilePath string,
) (fileModificationDate string, fileSize int64, fileTag string, err error) {
	return func(
		ctx context.Context,
		sourceFilePath string,
	) (string, int64, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, sourceFilePath, nil)
		if err != nil {
			return "", 0, "", fmt.Errorf("failed to create a new request: %w", err)
		}

		res, err := httClient.Do(req) //nolint:bodyclose
		if err != nil {
			return "", 0, "", fmt.Errorf("failed to HEAD the URL: %w", err)
		}

		defer utils.CloseOrLogError(ctx)(res.Body)

		fileModificationDate := ""
		fileSize := res.ContentLength
		fileTag := ""
		httpLastModified := res.Header.Get("Last-Modified")

		if httpLastModified != "" {
			var timeParsed time.Time
			timeParsed, err = time.Parse(time.RFC1123, httpLastModified)
			if err != nil {
				timeParsed, err = time.Parse(time.RFC1123Z, httpLastModified)
				if err != nil {
					return fileModificationDate, fileSize, fileTag, fmt.Errorf("failed to parse Last-Modified header: %w", err)
				}
			}

			fileModificationDate = timeParsed.UTC().Format(time.RFC3339)
		}

		httpTag := res.Header.Get("ETag")

		if httpTag != "" {
			httpTagParts := strings.Split(httpTag, "\"")

			if len(httpTagParts) > 1 {
				fileTag = httpTagParts[1]
			}
		}

		return fileModificationDate, fileSize, fileTag, nil
	}
}

func fileDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	config := m.(proxmoxtf.ProviderConfiguration)
	capi, err := config.GetClient()
	if err != nil {
		return diag.FromErr(err)
	}

	datastoreID := d.Get(mkResourceVirtualEnvironmentFileDatastoreID).(string)
	nodeName := d.Get(mkResourceVirtualEnvironmentFileNodeName).(string)

	err = capi.Node(nodeName).Storage(datastoreID).DeleteDatastoreFile(ctx, d.Id())
	if err != nil && !errors.Is(err, api.ErrResourceDoesNotExist) {
		return diag.FromErr(err)
	}

	d.SetId("")

	return nil
}

func fileUpdate(_ context.Context, _ *schema.ResourceData, _ interface{}) diag.Diagnostics {
	// a pass-through update function -- no actual resource update is needed / allowed
	// only the TF state is updated, for example, a timeout_upload attribute value
	return nil
}
