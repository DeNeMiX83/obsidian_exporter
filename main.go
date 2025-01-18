package main

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

type Config struct {
	ParseFilePath string   `yaml:"parseFilePath"`
	BlackList     []string `yaml:"blackList"`
	FilesPaths    []string `yaml:"filesPaths"`
	MediaPath     string   `yaml:"mediaPath"`
	LevelNesting  int      `yaml:"levelNesting"`
}

func GetConfig(path string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}

	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return cfg, err
	}

	return cfg, nil
}

func main() {
	var root cobra.Command
	cmd := &cobra.Command{
		Use:     "run",
		Aliases: []string{"r", "run"},
		Short:   "run shopper",
		Long:    "run shopper",
		Run:     run,
	}
	cmd.PersistentFlags().String("config", "", "--config <path to config>")
	root.AddCommand(cmd)

	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func run(cmd *cobra.Command, _ []string) {
	cfgPath := cmd.Flag("config").Value.String()
	cfg, err := GetConfig(cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll("выгрузка/вложенные файлы", os.ModePerm); err != nil {
		fmt.Sprintln("ошибка создания директории: %w", err)
	}
	parseFileBFS(cfg.ParseFilePath, cfg)

	sourceFolder := "выгрузка"
	destinationZip := "выгрузка.zip"

	err = ZipFolder(sourceFolder, destinationZip)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Архив создан успешно:", destinationZip)

	err = os.RemoveAll("выгрузка")
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Папка успешно удалена:")
}

func parseFileBFS(filename string, cfg Config) {
	queue := []struct {
		file  string
		level int
	}{{filename, 1}}
	parsedFiles := map[string]struct{}{}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.level > cfg.LevelNesting {
			continue
		}

		if slices.Contains(cfg.BlackList, current.file) {
			continue
		}
		if _, found := parsedFiles[current.file]; found {
			continue
		}
		parsedFiles[current.file] = struct{}{}

		var filePath string
		for _, sourceDir := range cfg.FilesPaths {
			var err error
			filePath, err = getFilePathByName(sourceDir, current.file)
			if err != nil {
				fmt.Println("Ошибка при поиске пути файла", err)
			}
			if filePath != "" {
				destPath := filepath.Join("выгрузка/вложенные файлы", current.file+".md")
				if err := copyFile(filePath, destPath); err != nil {
					fmt.Sprintln("ошибка копирования файла %s: %w", filePath, err)
				}
				break
			}
		}
		if filePath == "" {
			fmt.Printf("Файл %s не найден\n", current.file)
			continue
		}

		file, err := os.Open(filePath)
		if err != nil {
			fmt.Println("Ошибка открытия файла:", err)
			continue
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		var filesMatches []string
		var mediaMatches []string

		for scanner.Scan() {
			line := scanner.Text()
			filesMatches = append(filesMatches, nestingFiles(line)...)
			mediaMatches = append(mediaMatches, nestingMedia(line)...)
		}

		if err := scanner.Err(); err != nil {
			fmt.Println("Ошибка чтения файла:", err)
		}

		if err := copyMatchingFiles(cfg.MediaPath, "выгрузка/вложенные медиа", mediaMatches); err != nil {
			log.Printf("Ошибка при копировании медиафайлов из %s: %v\n", cfg.MediaPath, err)
		}

		for _, match := range filesMatches {
			queue = append(queue, struct {
				file  string
				level int
			}{match, current.level + 1})
		}
	}
}

// Функция для копирования только тех файлов из sourceDir, которые совпадают с элементами из matches
func copyMatchingFiles(sourceDir, nestingsDir string, matches []string) error {
	// Создаем директорию, если она не существует
	if err := os.MkdirAll(nestingsDir, os.ModePerm); err != nil {
		return fmt.Errorf("ошибка создания директории: %w", err)
	}

	// Множество для быстрого поиска совпадений (без расширений)
	matchSet := make(map[string]struct{})
	for _, match := range matches {
		matchSet[match] = struct{}{}
	}

	// Проходим по всем файлам и подкаталогам
	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Если это файл и его имя (без расширения) совпадает с одним из matches, копируем его
		if !info.IsDir() {
			fileName := info.Name()
			fileNameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
			if _, found := matchSet[fileNameWithoutExt]; found {
				// Определяем путь для копии
				destPath := filepath.Join(nestingsDir, fileName)
				if err := copyFile(path, destPath); err != nil {
					return fmt.Errorf("ошибка копирования файла %s: %w", path, err)
				}
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("ошибка обхода директории %s: %w", sourceDir, err)
	}
	return nil
}

func getFilePathByName(sourceDir string, filename string) (string, error) {
	// Проходим по всем файлам и подкаталогам
	var filePath string
	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Если это файл и его имя (без расширения) совпадает с одним из matches, копируем его
		if !info.IsDir() {
			fileName := info.Name()
			fileNameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
			normalizedFileName := norm.NFC.String(fileNameWithoutExt)
			normalizedFileNameWithoutExt := norm.NFC.String(filename)
			if normalizedFileName == normalizedFileNameWithoutExt {
				filePath = path
			}
		}
		return nil
	})

	return filePath, err
}

// Вспомогательная функция для копирования файла
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// Функция для извлечения текста внутри [[...]], игнорируя строки с "Pasted image"
func nestingFiles(content string) []string {
	reFilesBrackets := regexp.MustCompile(`\[\[(.*?)\]\]`)
	var matches []string

	// Ищем совпадения для [[...]]
	filesMatches := reFilesBrackets.FindAllStringSubmatch(content, -1)
	for _, match := range filesMatches {
		if len(match) > 1 {
			// Игнорируем строки с "Pasted image"
			if isImageFile(match[1]) {
				continue
			}
			// Обрезаем часть после "|", если она присутствует
			if idx := strings.Index(match[1], "|"); idx != -1 {
				matches = append(matches, match[1][:idx])
			} else {
				matches = append(matches, match[1])
			}
		}
	}

	return matches
}

func isImageFile(filename string) bool {
	imageExtensions := []string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".tiff", ".svg"}
	ext := strings.ToLower(filepath.Ext(filename))
	for _, imageExt := range imageExtensions {
		if ext == imageExt {
			return true
		}
	}
	return false
}

// Функция для извлечения текста внутри ![[...|...]] (|... может быть числом или отсутствовать)
func nestingMedia(content string) []string {
	reMedisBrackets := regexp.MustCompile(`!\[\[(.*?)(\|[0-9]*)?\]\]`)
	var matches []string

	// Ищем совпадения для ![[...|...]]
	mediaMatches := reMedisBrackets.FindAllStringSubmatch(content, -1)
	for _, match := range mediaMatches {
		if len(match) > 1 {
			// Убираем расширение у имени файла, если оно присутствует
			fileNameWithoutExt := strings.TrimSuffix(match[1], filepath.Ext(match[1]))
			matches = append(matches, fileNameWithoutExt)
		}
	}

	return matches
}

func ZipFolder(source, target string) error {
	// Создаем выходной файл ZIP
	zipFile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	// Создаем новый zip.Writer для записи файлов в ZIP
	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	// Пройдемся по всем файлам и папкам внутри source
	err = filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Пропускаем саму папку, которая является корневой
		if path == source {
			return nil
		}

		// Получаем относительный путь файла внутри архива
		relPath, err := filepath.Rel(filepath.Dir(source), path)
		if err != nil {
			return err
		}

		// Если это директория, добавляем её в архив без содержимого
		if info.IsDir() {
			_, err := archive.Create(relPath + "/")
			return err
		}

		// Создаем файл внутри ZIP-архива
		zipFile, err := archive.Create(relPath)
		if err != nil {
			return err
		}

		// Открываем исходный файл для копирования в архив
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		// Копируем содержимое файла в ZIP-архив
		_, err = io.Copy(zipFile, srcFile)
		return err
	})

	return err
}
