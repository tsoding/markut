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

		result = append(result, ts+delay)
	}

	return result
}

func ffmpegCutChunk(inputPath string, startSecs int, durationSecs int, outputPath string) error {
	cmd := exec.Command(
		"ffmpeg",
		"-ss", strconv.Itoa(startSecs),
		"-i", inputPath,
		"-c", "copy",
		"-t", strconv.Itoa(durationSecs),
		outputPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegConcatChunks(listPath string, outputPath string) {
	cmd := exec.Command(
		"ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		outputPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
	fmt.Printf("Usage: markut <SUBCOMMAND> [OPTIONS]\n")
	fmt.Printf("SUBCOMMANDS:\n")
	fmt.Printf("  final      Render the final video\n")
	fmt.Printf("  chunk      Render specific chunk of the final video\n")
}

func subUsage(subName string, subFlag *flag.FlagSet) {
	fmt.Printf("Usage: markut %s [OPTIONS]\n", subName)
	fmt.Printf("OPTIONS:\n")
	subFlag.PrintDefaults()
}

func finalSubcommand(args []string) {
	finalFlag := flag.NewFlagSet("final", flag.ExitOnError)
	csvPtr := finalFlag.String("csv", "", "Path to the CSV file with markers")
	inputPtr := finalFlag.String("input", "", "Path to the input video file")
	delayPtr := finalFlag.Int("delay", 0, "Delay of markers in seconds")

	finalFlag.Parse(args)

	if *csvPtr == "" {
		subUsage("final", finalFlag)
		fmt.Printf("ERROR: No -csv file is provided\n")
		os.Exit(1)
	}

	if *inputPtr == "" {
		subUsage("final", finalFlag)
		fmt.Printf("ERROR: No -input file is provided\n")
		os.Exit(1)
	}

	ts := loadTsFromFile(*csvPtr, *delayPtr)
	n := len(ts)
	if n%2 != 0 {
		fmt.Printf("ERROR: The amount of markers must be even")
		os.Exit(1)
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
		chunkName := fmt.Sprintf("chunk-%02d.mp4", i)
		err := ffmpegCutChunk(*inputPtr, start, duration, chunkName)
		if err != nil {
			fmt.Printf("WARNING! Failed to cut chunk: %s", err)
		}
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

func chunkSubcommand(args []string) {
	chunkFlag := flag.NewFlagSet("chunk", flag.ExitOnError)
	csvPtr := chunkFlag.String("csv", "", "Path to the CSV file with markers")
	inputPtr := chunkFlag.String("input", "", "Path to the input video file")
	delayPtr := chunkFlag.Int("delay", 0, "Delay of markers in seconds")
	chunkPtr := chunkFlag.Int("chunk", 0, "Chunk number to render")

	chunkFlag.Parse(args)

	if *csvPtr == "" {
		subUsage("chunk", chunkFlag)
		fmt.Printf("ERROR: No -csv file is provided\n")
		os.Exit(1)
	}

	if *inputPtr == "" {
		subUsage("chunk", chunkFlag)
		fmt.Printf("ERROR: No -input file is provided\n")
		os.Exit(1)
	}

	ts := loadTsFromFile(*csvPtr, *delayPtr)
	n := len(ts)
	if n%2 != 0 {
		fmt.Printf("ERROR: The amount of markers must be even")
		os.Exit(1)
	}

	chunkCount := n / 2

	if *chunkPtr > chunkCount {
		fmt.Printf("ERROR: %d is incorrect chunk number. There is only %d of them.\n", *chunkPtr, chunkCount)
		os.Exit(1)
	}

	start := ts[*chunkPtr*2+0]
	end := ts[*chunkPtr*2+1]
	duration := end - start
	chunkName := fmt.Sprintf("chunk-%02d.mp4", *chunkPtr)
	err := ffmpegCutChunk(*inputPtr, start, duration, chunkName)
	panic_if_err(err)

	fmt.Printf("%s is rendered!\n", chunkName)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		fmt.Printf("ERROR: No subcommand is provided\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "final":
		finalSubcommand(os.Args[2:])
	case "chunk":
		chunkSubcommand(os.Args[2:])
	default:
		usage()
		fmt.Printf("Unknown subcommand %s\n", os.Args[1])
		os.Exit(1)
	}
}
