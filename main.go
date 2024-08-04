package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
)

const (
	fontSize = 24
	dpi      = 72
	padding  = 10
	fontURL  = "https://github.com/chrissimpkins/codeface/raw/master/fonts/liberation-mono/LiberationMono-Regular.ttf"
	fontPath = "/tmp/LiberationMono-Regular.ttf"
)

func generateRandomHash(n int) string {
	bytes := make([]byte, n)
	_, err := rand.Read(bytes)
	if err != nil {
		log.Fatalf("Failed to generate random hash: %v", err)
	}
	return hex.EncodeToString(bytes)[:n]
}

// CleanText replaces non-printable characters with spaces
func CleanText(text string) string {
	var cleanText strings.Builder
	for _, r := range text {
		if r >= 32 && r <= 126 { // Printable ASCII range
			cleanText.WriteRune(r)
		} else if r == '\n' { // Keep newlines
			cleanText.WriteRune(r)
		} else {
			cleanText.WriteRune(' ') // Replace other characters with space
		}
	}
	return cleanText.String()
}

func downloadFont() error {
	resp, err := http.Get(fontURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	fontFile, err := os.Create(fontPath)
	if err != nil {
		return err
	}
	defer fontFile.Close()

	_, err = io.Copy(fontFile, resp.Body)
	return err
}

func parseHexColor(s string) (color.RGBA, error) {
	c := color.RGBA{A: 0xff}
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return c, fmt.Errorf("invalid length, must be 6 hex characters")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return c, err
	}
	c.R, c.G, c.B = b[0], b[1], b[2]
	return c, nil
}

func uploadToS3(filename string) error {
	awsAccessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	awsSecretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	bucketName := os.Getenv("AWS_BUCKET_NAME")
	awsRegion := os.Getenv("AWS_REGION")

	if awsAccessKey == "" || awsSecretKey == "" || bucketName == "" || awsRegion == "" {
		log.Fatalf("AWS credentials, bucket name, or region are not set in environment variables")
	}

	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(awsRegion),
		Credentials: credentials.NewStaticCredentials(awsAccessKey, awsSecretKey, ""),
	})
	if err != nil {
		return err
	}

	svc := s3.New(sess)

	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = svc.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(filepath.Base(filename)),
		Body:   file,
	})
	return err
}

func main() {
	// Define flags
	outputDirFlag := flag.String("oD", "", "Directory to save the output image")
	outputFileFlag := flag.String("oF", "", "Name of the output image file")
	uploadFlag := flag.Bool("uP", false, "Upload the image to an S3 bucket")
	fontColorFlag := flag.String("fC", "FFFFFF", "Font color in hex (default: white)")
	bgColorFlag := flag.String("bC", "181414", "Background color in hex (default: dark gray)")
	flag.Parse()

	// Read text content from stdin
	var textBuilder strings.Builder
	_, err := io.Copy(&textBuilder, os.Stdin)
	if err != nil {
		log.Fatalf("Failed to read text content: %v", err)
	}
	text := textBuilder.String()

	// Clean the text
	text = CleanText(text)

	// Check if the font file is present in /tmp
	if _, err := os.Stat(fontPath); os.IsNotExist(err) {
		// Download the font file
		log.Println("Font file not found, downloading...")
		err = downloadFont()
		if err != nil {
			log.Fatalf("Failed to download font file: %v", err)
		}
	}

	// Load the font
	fontBytes, err := ioutil.ReadFile(fontPath)
	if err != nil {
		log.Fatalf("Failed to read font file: %v", err)
	}
	f, err := freetype.ParseFont(fontBytes)
	if err != nil {
		log.Fatalf("Failed to parse font: %v", err)
	}

	// Parse colors from flags
	fontColor, err := parseHexColor(*fontColorFlag)
	if err != nil {
		log.Fatalf("Invalid font color: %v", err)
	}

	bgColor, err := parseHexColor(*bgColorFlag)
	if err != nil {
		log.Fatalf("Invalid background color: %v", err)
	}

	// Create a freetype context for font measurements
	c := freetype.NewContext()
	c.SetDPI(dpi)
	c.SetFont(f)
	c.SetFontSize(fontSize)
	c.SetSrc(image.NewUniform(fontColor))

	// Create a drawer for measuring text
	drawer := &font.Drawer{
		Face: truetype.NewFace(f, &truetype.Options{Size: fontSize}),
	}

	// Measure text to calculate image size
	lines := strings.Split(text, "\n")
	var maxWidth int
	for _, line := range lines {
		width := drawer.MeasureString(line).Ceil()
		if width > maxWidth {
			maxWidth = width
		}
	}
	lineHeight := drawer.Face.Metrics().Height.Ceil()
	imgWidth := maxWidth + 2*padding
	imgHeight := lineHeight*len(lines) + 2*padding

	// Create a blank RGBA image
	img := image.NewRGBA(image.Rect(0, 0, imgWidth, imgHeight))

	// Fill the background with the specified color
	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	// Set the clip and destination for drawing text
	c.SetClip(img.Bounds())
	c.SetDst(img)

	// Draw the text
	pt := freetype.Pt(padding, padding+int(c.PointToFixed(fontSize)>>6))
	for _, line := range lines {
		if line == "" {
			pt.Y += c.PointToFixed(fontSize) // Handle empty lines
			continue
		}
		_, err = c.DrawString(line, pt)
		if err != nil {
			log.Fatalf("Failed to draw string: %v", err)
		}
		pt.Y += c.PointToFixed(fontSize)
	}

	// Generate a random 5-character hash for the image name if outputFile flag is not provided
	imageName := *outputFileFlag
	if imageName == "" {
		imageName = generateRandomHash(5) + ".png"
	} else {
		imageName += ".png"
	}

	// Check if the outputDir flag is provided
	outputDir := *outputDirFlag
	if outputDir == "" {
		// Save the image in the current directory
		outputDir = "."
	}

	// Create the full path for the image
	imagePath := filepath.Join(outputDir, imageName)

	// Save the image to a file
	file, err := os.Create(imagePath)
	if err != nil {
		log.Fatalf("Failed to create image file: %v", err)
	}
	defer file.Close()

	err = png.Encode(file, img)
	if err != nil {
		log.Fatalf("Failed to encode image: %v", err)
	}

	log.Printf("Image saved to %s\n", imagePath)

	// Upload to S3 if the flag is set
	if *uploadFlag {
		err := uploadToS3(imagePath)
		if err != nil {
			log.Fatalf("Failed to upload image to S3: %v", err)
		}
		log.Printf("Image uploaded to S3 bucket\n")
	}
}
