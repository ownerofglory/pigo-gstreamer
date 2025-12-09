package main

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"runtime"
	"time"

	pigo "github.com/esimov/pigo/core"
)

var (
	classifier *pigo.Pigo
	cascade    []byte
)

func loadCascade(path string) *pigo.Pigo {
	if len(cascade) != 0 && classifier != nil {
		return classifier
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Error reading the cascade file: %v", err)
	}
	cascade = data

	p := pigo.NewPigo()
	classifier, err = p.Unpack(cascade)
	if err != nil {
		log.Fatalf("Error unpacking the cascade file: %v", err)
	}
	return classifier
}

func detectFaces(classifier *pigo.Pigo, pixels []uint8, rows, cols int) []pigo.Detection {
	cParams := pigo.CascadeParams{
		MinSize:     100,
		MaxSize:     600,
		ShiftFactor: 0.15,
		ScaleFactor: 1.1,
		ImageParams: pigo.ImageParams{
			Pixels: pixels,
			Rows:   rows,
			Cols:   cols,
			Dim:    cols,
		},
	}
	dets := classifier.RunCascade(cParams, 0.0)
	dets = classifier.ClusterDetections(dets, 0.0)
	return dets
}

// clamp 0..255
func clamp(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// drawBoxGray draws a rectangle on a GRAY8 buffer.
func drawBoxGray(buf []byte, width, height int, cx, cy, radius int) {
	x0 := cx - radius
	y0 := cy - radius
	x1 := cx + radius
	y1 := cy + radius

	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 >= width {
		x1 = width - 1
	}
	if y1 >= height {
		y1 = height - 1
	}

	// top & bottom
	for x := x0; x <= x1; x++ {
		iTop := y0*width + x
		iBot := y1*width + x
		buf[iTop] = clamp(int(buf[iTop]) + 80)
		buf[iBot] = clamp(int(buf[iBot]) + 80)
	}

	// left & right
	for y := y0; y <= y1; y++ {
		iL := y*width + x0
		iR := y*width + x1
		buf[iL] = clamp(int(buf[iL]) + 80)
		buf[iR] = clamp(int(buf[iR]) + 80)
	}
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	log.SetOutput(os.Stderr)

	width := flag.Int("width", 640, "frame width (pixels)")
	height := flag.Int("height", 480, "frame height (pixels)")
	cascadePath := flag.String("cascade", "cascade/facefinder", "path to Pigo cascade file")
	minScore := flag.Float64("min-score", 5.0, "minimum detection score (Q) to report")
	flag.Parse()

	if *width <= 0 || *height <= 0 {
		log.Fatal("width and height must be > 0")
	}

	clf := loadCascade(*cascadePath)
	log.Println("Loaded Pigo cascade from", *cascadePath)

	frameSize := (*width) * (*height)
	buf := make([]byte, frameSize)

	r := bufio.NewReader(os.Stdin)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	log.Printf("Pigo filter: expecting GRAY8 frames of %dx%d (%d bytes)\n",
		*width, *height, frameSize)

	frameCount := 0
	start := time.Now()

	for {
		n, err := io.ReadFull(r, buf)
		if err != nil {
			if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
				log.Println("EOF on stdin, exiting")
				return
			}
			log.Fatalf("read error: %v", err)
		}
		if n != frameSize {
			log.Fatalf("short read: got %d, expected %d", n, frameSize)
		}

		frameCount++

		// Run Pigo
		dets := detectFaces(clf, buf, *height, *width)
		for _, det := range dets {
			if det.Q >= float32(*minScore) {
				log.Printf("frame=%d face row=%d col=%d scale=%d q=%.2f",
					frameCount, det.Row, det.Col, det.Scale, det.Q)

				// Draw into the buffer (so it shows up in the video)
				radius := det.Scale / 2
				drawBoxGray(buf, *width, *height, det.Col, det.Row, radius)
			}
		}

		// write modified frame to stdout
		m, err := w.Write(buf)
		if err != nil {
			log.Fatalf("write error: %v", err)
		}
		if m != frameSize {
			log.Fatalf("short write: wrote %d, expected %d", m, frameSize)
		}
		if err := w.Flush(); err != nil {
			log.Fatalf("flush error: %v", err)
		}

		if frameCount%60 == 0 {
			elapsed := time.Since(start).Seconds()
			fps := float64(frameCount) / elapsed
			log.Printf("Processed %d frames (%.1f FPS)", frameCount, fps)
		}
	}
}
