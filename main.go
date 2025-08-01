package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

type ReplaceConfig map[string]map[string]string // "filename#sheet" -> key -> value

// 解析配置文件
func parseConfig(configPath string) (ReplaceConfig, error) {
	f, err := excelize.OpenFile(configPath)
	if err != nil {
		return nil, err
	}

	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("配置文件缺少数据")
	}

	headers := rows[0]
	config := make(ReplaceConfig)

	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		key := row[0]
		for colIdx, colHeader := range headers[1:] {
			if strings.TrimSpace(colHeader) == "" {
				continue
			}
			if _, ok := config[colHeader]; !ok {
				config[colHeader] = make(map[string]string)
			}
			val := ""
			if colIdx+1 < len(row) {
				val = row[colIdx+1]
			}
			config[colHeader][key] = val // 空值直接存 ""
		}
	}
	return config, nil
}

// 替换模板内容
func processTemplate(fileBytes []byte, filename string, cfg ReplaceConfig) ([]byte, error) {
	var replacements []struct {
		sheet string
		rules map[string]string
	}

	for header, rules := range cfg {
		if strings.HasPrefix(header, filename+"#") {
			sheet := strings.SplitN(header, "#", 2)[1]
			replacements = append(replacements, struct {
				sheet string
				rules map[string]string
			}{sheet: sheet, rules: rules})
		}
	}
	if len(replacements) == 0 {
		return nil, fmt.Errorf("没有找到 %s 的配置", filename)
	}

	tmpPath := filepath.Join(os.TempDir(), filename)
	os.WriteFile(tmpPath, fileBytes, 0644)
	f, err := excelize.OpenFile(tmpPath)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpPath)

	for _, rep := range replacements {
		index, err := f.GetSheetIndex(rep.sheet)
		if index == -1 || err != nil {
			fmt.Println("err: ", err)
			fmt.Println("sheet: ", rep.sheet)
			continue
		}
		rows, _ := f.GetRows(rep.sheet)
		for rIdx, row := range rows {
			for cIdx, cell := range row {
				newVal := cell
				for k, v := range rep.rules {
					if k != "" && strings.Contains(newVal, k) {
						newVal = strings.ReplaceAll(newVal, k, v)
					}
				}
				cellName, _ := excelize.CoordinatesToCellName(cIdx+1, rIdx+1)
				f.SetCellValue(rep.sheet, cellName, newVal)
			}
		}
	}

	var buf bytes.Buffer
	f.Write(&buf)
	return buf.Bytes(), nil
}

func main() {
	r := gin.Default()
	r.Static("/static", "./static") // 让 HTML 可访问

	// 上传接口
	r.POST("/replace_excel", func(c *gin.Context) {
		configFile, err := c.FormFile("config")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少配置文件"})
			return
		}
		configPath := filepath.Join(os.TempDir(), configFile.Filename)
		c.SaveUploadedFile(configFile, configPath)

		cfg, err := parseConfig(configPath)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		form, _ := c.MultipartForm()
		files := form.File["templates"]
		if len(files) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "没有上传模板文件"})
			return
		}

		results := make(map[string][]byte)
		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, file := range files {
			wg.Add(1)
			fmt.Println("----", file.Filename)
			go func(fh *multipart.FileHeader) {
				defer wg.Done()
				fObj, _ := fh.Open()
				data, _ := io.ReadAll(fObj)
				fObj.Close()

				output, err := processTemplate(data, fh.Filename, cfg)
				if err == nil {
					mu.Lock()
					results[fh.Filename] = output
					mu.Unlock()
				}
			}(file)
		}
		wg.Wait()

		// 打包ZIP
		var zipBuf bytes.Buffer
		zipWriter := zip.NewWriter(&zipBuf)
		for fname, content := range results {
			w, _ := zipWriter.Create(strings.TrimSuffix(fname, ".xlsx") + "_filled.xlsx")
			w.Write(content)
		}
		zipWriter.Close()

		c.Header("Content-Disposition", "attachment; filename=results.zip")
		c.Data(http.StatusOK, "application/zip", zipBuf.Bytes())
	})

	fmt.Println("🚀 服务器已启动: http://localhost:8080")
	r.Run(":8080")
}
