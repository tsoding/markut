package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func panic_if_err(err error) {
	if err != nil {
		panic(err)
	}
}

func tsToSecs(ts string) int {
	comps := strings.Split(ts, ":")
	if len(comps) != 3 {
		panic("Expected 3 components in the timestamp")
	}

	hh, err := strconv.Atoi(comps[0])
	panic_if_err(err)
	mm, err := strconv.Atoi(comps[1])
	panic_if_err(err)
	ss, err := strconv.Atoi(comps[2])
	panic_if_err(err)

	return 60*60*hh + 60*mm + ss
}

func secsToTs(secs int) string {
	hh := secs / 60 / 60
	mm := secs / 60 % 60
	ss := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", hh, mm, ss)
}

func loadTsFromFile(path string, delay int) []int {
	f, err := os.Open(path)
	panic_if_err(err)
	defer f.Close()

	r := csv.NewReader(f)

	var result []int

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		panic_if_err(err)

		ts, err := strconv.Atoi(record[0])
		panic_if_err(err)

		result = append(result, ts + delay)
	}

	return result
}

func ffmpegCutChunk(inputPath string, startSecs int, durationSecs int, outputPath string) {
	cmd := exec.Command(
		"ffmpeg",
		"-ss", strconv.Itoa(startSecs),
		"-i", inputPath,
		"-c", "copy",
		"-t", strconv.Itoa(durationSecs),
		outputPath)
    cmd.Stdin  = os.Stdin;
    cmd.Stdout = os.Stdout;
    cmd.Stderr = os.Stderr;
	err := cmd.Run()
	panic_if_err(err)
}

func ffmpegConcatChunks(listPath string, outputPath string) {
	cmd := exec.Command(
		"ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		outputPath)
    cmd.Stdin  = os.Stdin;
    cmd.Stdout = os.Stdout;
    cmd.Stderr = os.Stderr;
	err := cmd.Run()
	panic_if_err(err)
}

func ffmpegGenerateConcatList(chunkNames []string, outputPath string) {
	f, err := os.Create(outputPath)
	panic_if_err(err)
	defer f.Close()

	for _, name := range chunkNames {
		fmt.Fprintf(f, "file '%s'\n", name)
	}
}

func usage() {
	fmt.Printf("Usage: markut [OPTIONS]\n")
	fmt.Printf("OPTIONS:\n")
	flag.PrintDefaults()
}

func main() {
	csvPtr := flag.String("csv", "", "Path to the CSV file with markers")
	inputPtr := flag.String("input", "", "Path to the input video file")
	delayPtr := flag.Int("delay", 0, "Delay of markers in seconds")

	flag.Parse()

	if *csvPtr == "" {
		usage()
		panic("No -csv file is provided")
	}

	if *inputPtr == "" {
		usage()
		panic("No -input file is provided")
	}

	ts := loadTsFromFile(*csvPtr, *delayPtr)
	n := len(ts)
	if n%2 != 0 {
		panic("The amount of markers must be even")
	}

	secs := 0
	cutsTs := []string{}
	chunkNames := []string{}
	for i := 0; i < n/2; i += 1 {
		start := ts[i*2+0]
		end := ts[i*2+1]
		duration := end - start
		secs += duration
		cutsTs = append(cutsTs, secsToTs(secs))
		chunkName := "chunk-%02d.mp4"
		ffmpegCutChunk(*inputPtr, start, duration, fmt.Sprintf(chunkName, i))
		chunkNames = append(chunkNames, chunkName)
	}

	ourlistPath := "ourlist.txt"
	ffmpegGenerateConcatList(chunkNames, ourlistPath)
	ffmpegConcatChunks(ourlistPath, "output.mp4")

	fmt.Println("Timestamps of cuts:")
	for _, cut := range cutsTs {
		fmt.Println(cut)
	}
}
