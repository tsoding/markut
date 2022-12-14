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
	"path"
)

type Secs = float64

func tsToSecs(ts string) (Secs, error) {
	var err error = nil
	var mm, hh int = 0, 0
	var ss Secs = 0
	var index = 0
	switch comps := strings.Split(ts, ":"); len(comps) {
	case 3:
		hh, err = strconv.Atoi(comps[index])
		if err != nil { return 0, err }
		index += 1
		fallthrough
	case 2:
		mm, err = strconv.Atoi(comps[index])
		if err != nil { return 0, err }
		index += 1
		fallthrough
	case 1:
		ss, err = strconv.ParseFloat(comps[index], 64)
		if err != nil { return 0, err }
		return 60*60*Secs(hh) + 60*Secs(mm) + ss, nil
	default:
		return 0, fmt.Errorf("Unexpected amount of components in the timestamp (%d)", len(comps))
	}
}

// TODO: Make secsToTs accept float instead of int
func secsToTs(secs int) string {
	hh := secs / 60 / 60
	mm := secs / 60 % 60
	ss := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", hh, mm, ss)
}

type Chunk struct {
	Start   Secs
	End     Secs
	Name    string
}

func (chunk Chunk) Duration() Secs {
	return chunk.End - chunk.Start
}

func loadChunksFromFile(path string, delay Secs) ([]Chunk, error) {
	var chunks []Chunk

	f, err := os.Open(path)
	if err != nil {
		return chunks, err
	}
	defer f.Close()

	r := csv.NewReader(f)

	var chunkCurrent *Chunk = nil

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return chunks, err
		}
		if len(record) <= 0 {
			return chunks, fmt.Errorf("CSV record must have at least one field")
		}

		timestamp, err := tsToSecs(record[0])
		if err != nil {
			return chunks, err
		}

		timestamp += delay

		if chunkCurrent == nil {
			chunkCurrent = &Chunk{
				Start: timestamp,
			}
		} else {
			if chunkCurrent.Start > timestamp {
				return chunks, fmt.Errorf("Chunk %02d ends earlier than starts", len(chunks))
			}

			chunkCurrent.Name = fmt.Sprintf("chunk-%02d.mp4", len(chunks))
			chunkCurrent.End = timestamp
			chunks = append(chunks, *chunkCurrent)
			chunkCurrent = nil
		}
	}

	if chunkCurrent != nil {
		return chunks, fmt.Errorf("Unclosed chunk detected! Please make sure that there is an even amount of markers.")
	}

	return chunks, nil
}

func ffmpegPathToBin() (ffmpegPath string) {
	ffmpegPath = "ffmpeg"
	ffmpegPrefix, ok := os.LookupEnv("FFMPEG_PREFIX")
	if ok {
		ffmpegPath = path.Join(ffmpegPrefix, "bin", "ffmpeg")
	}
	return
}

func logCmd(name string, args ...string) {
	chunks := []string{}
	chunks = append(chunks, name)
	for _, arg := range args {
		if strings.Contains(arg, " ") {
			// TODO: use proper shell escaping instead of just wrapping with double quotes
			chunks = append(chunks, "\""+arg+"\"")
		} else {
			chunks = append(chunks, arg)
		}
	}
	fmt.Printf("[CMD] %s\n", strings.Join(chunks, " "))
}

func ffmpegCutChunk(inputPath string, chunk Chunk, y bool) error {
	ffmpeg := ffmpegPathToBin()
	args := []string{}

	if y {
		args = append(args, "-y")
	}

	args = append(args, "-ss", strconv.FormatFloat(chunk.Start, 'f', -1, 64))
	args = append(args, "-i", inputPath)
	args = append(args, "-c", "copy")
	args = append(args, "-t", strconv.FormatFloat(chunk.Duration(), 'f', -1, 64))
	args = append(args, chunk.Name)

	logCmd(ffmpeg, args...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegConcatChunks(listPath string, outputPath string, y bool) error {
	ffmpeg := ffmpegPathToBin()
	args := []string{}

	if y {
		args = append(args, "-y")
	}

	args = append(args, "-f", "concat")
	args = append(args, "-safe", "0")
	args = append(args, "-i", listPath)
	args = append(args, "-c", "copy")
	args = append(args, outputPath)

	logCmd(ffmpeg, args...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegFixupInput(inputPath, outputPath string, y bool) error {
	ffmpeg := ffmpegPathToBin()
	args := []string{}

	if y {
		args = append(args, "-y")
	}

	// ffmpeg -y -i {{ morning_input }} -codec copy -bsf:v h264_mp4toannexb {{ morning_input }}.fixed.ts
	args = append(args, "-i", inputPath)
	args = append(args, "-codec", "copy")
	args = append(args, "-bsf:v", "h264_mp4toannexb")
	args = append(args, outputPath)
	logCmd(ffmpeg, args...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegGenerateConcatList(chunks []Chunk, outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, chunk := range chunks {
		fmt.Fprintf(f, "file '%s'\n", chunk.Name)
	}

	return nil
}

type Highlight struct {
	timestamp string
	message   string
}

func highlightChunks(chunks []Chunk) []Highlight {
	secs := 0.0
	highlights := []Highlight{}

	for _, chunk := range chunks {
		highlights = append(highlights, Highlight{
			timestamp: secsToTs(int(secs + chunk.Duration())),
			message:   "cut",
		})

		secs += chunk.Duration()
	}

	return highlights
}

func finalSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("final", flag.ContinueOnError)
	csvPtr := subFlag.String("csv", "", "Path to the CSV file with markers (mandatory)")
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
	delayPtr := subFlag.Float64("delay", 0, "Delay of markers in seconds")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *csvPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -csv file is provided")
	}

	if *inputPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -input file is provided")
	}

	chunks, err := loadChunksFromFile(*csvPtr, *delayPtr)
	if err != nil {
		return fmt.Errorf("Could not load chunks from file %s: %w", *csvPtr, err)
	}
	for _, chunk := range chunks {
		err := ffmpegCutChunk(*inputPtr, chunk, *yPtr)
		if err != nil {
			fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name, err)
		}
	}

	listPath := "final-list.txt"
	ffmpegGenerateConcatList(chunks, listPath)
	ffmpegConcatChunks(listPath, "output.mp4", *yPtr)

	fmt.Println("Highlights:")
	for _, highlight := range highlightChunks(chunks) {
		fmt.Printf("%s - %s\n", highlight.timestamp, highlight.message)
	}

	return nil
}

func cutSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("cut", flag.ContinueOnError)
	csvPtr := subFlag.String("csv", "", "Path to the CSV file with markers (mandatory)")
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
	delayPtr := subFlag.Float64("delay", 0, "Delay of markers in seconds")
	cutPtr := subFlag.Int("cut", 0, "Cut number to render")
	padPtr := subFlag.Float64("pad", 2, "Amount of seconds to pad around the cut")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *csvPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -csv file is provided")
	}

	if *inputPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -input file is provided")
	}

	chunks, err := loadChunksFromFile(*csvPtr, *delayPtr)
	if err != nil {
		return fmt.Errorf("Could not load chunks from file %s: %w", *csvPtr, err)
	}

	if *cutPtr + 1 >= len(chunks) {
		return fmt.Errorf("%d is an invalid cut number. There is only %d of them.", *cutPtr, len(chunks) - 1);
	}

	cutChunks := []Chunk{
		{
			Start: chunks[*cutPtr].End - *padPtr,
			End: chunks[*cutPtr].End,
			Name: fmt.Sprintf("cut-%02d-left.mp4", *cutPtr),
		},
		{
			Start: chunks[*cutPtr + 1].Start,
			End: chunks[*cutPtr + 1].Start + *padPtr,
			Name: fmt.Sprintf("cut-%02d-right.mp4", *cutPtr),
		},
	}

	for _, chunk := range cutChunks {
		err := ffmpegCutChunk(*inputPtr, chunk, *yPtr)
		if err != nil {
			fmt.Printf("WARNING: Failed to cut chunk %s: %s\n", chunk.Name, err)
		}
	}

	listPath := fmt.Sprintf("cut-%02d-list.txt", *cutPtr);
	ffmpegGenerateConcatList(cutChunks, listPath)
	ffmpegConcatChunks(listPath, fmt.Sprintf("cut-%02d.mp4", *cutPtr), *yPtr)

	return nil
}

func chunkSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("chunk", flag.ContinueOnError)
	csvPtr := subFlag.String("csv", "", "Path to the CSV file with markers (mandatory)")
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
	delayPtr := subFlag.Float64("delay", 0, "Delay of markers in seconds")
	chunkPtr := subFlag.Int("chunk", 0, "Chunk number to render")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

	err := subFlag.Parse(args)

	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *csvPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -csv file is provided")
	}

	if *inputPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -input file is provided")
	}

	chunks, err := loadChunksFromFile(*csvPtr, *delayPtr)
	if err != nil {
		return fmt.Errorf("Could not load chunks from file %s: %w", *csvPtr, err)
	}

	if *chunkPtr > len(chunks) {
		return fmt.Errorf("%d is an incorrect chunk number. There is only %d of them.", *chunkPtr, len(chunks))
	}

	chunk := chunks[*chunkPtr]

	err = ffmpegCutChunk(*inputPtr, chunk, *yPtr)
	if err != nil {
		return fmt.Errorf("Could not cut the chunk %s: %s", chunk.Name, err)
	}

	fmt.Printf("%s is rendered!\n", chunk.Name)
	return nil
}

func inspectSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("inspect", flag.ContinueOnError)
	csvPtr := subFlag.String("csv", "", "Path to the CSV file with markers (mandatory)")
	delayPtr := subFlag.Float64("delay", 0, "Delay of markers in seconds")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *csvPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -csv file is provided")
	}

	chunks, err := loadChunksFromFile(*csvPtr, *delayPtr)
	if err != nil {
		return fmt.Errorf("Could not load chunks from file %s: %w", *csvPtr, err)
	}
	fmt.Println("Chunks:")
	for _, chunk := range chunks {
		fmt.Printf("  Name:  %s\n", chunk.Name)
		fmt.Printf("  Start: %s (%s)\n", secsToTs(int(chunk.Start)), strconv.FormatFloat(chunk.Start, 'f', -1, 64))
		fmt.Printf("  End:   %s (%s)\n", secsToTs(int(chunk.End)), strconv.FormatFloat(chunk.End, 'f', -1, 64))
	}

	fmt.Println("Cuts:")
	for _, highlight := range highlightChunks(chunks) {
		fmt.Printf("  %s - %s\n", highlight.timestamp, highlight.message)
	}
	return nil
}

func fixupSubcommand(args []string) error {
	subFlag := flag.NewFlagSet("fixup", flag.ExitOnError)
	inputPtr := subFlag.String("input", "", "Path to the input video file (mandatory)")
	yPtr := subFlag.Bool("y", false, "Pass -y to ffmpeg")

	err := subFlag.Parse(args)
	if err == flag.ErrHelp {
		return nil
	}

	if err != nil {
		return err
	}

	if *inputPtr == "" {
		subFlag.Usage()
		return fmt.Errorf("No -input file is provided")
	}

	outputPath := "input.ts"
	ffmpegFixupInput(*inputPtr, outputPath, *yPtr)
	fmt.Printf("Generated %s\n", outputPath)

	return nil
}

type Subcommand struct {
	Name string
	Run func(args []string) error
	Description string
}

var Subcommands = []Subcommand{
	{
		Name: "fixup",
		Run: fixupSubcommand,
		Description: "Fixup the initial footage",
	},
	{
		Name: "cut",
		Run: cutSubcommand,
		Description: "Render specific cut of the final video",
	},
	{
		Name: "chunk",
		Run: chunkSubcommand,
		Description: "Render specific chunk of the final video",
	},
	{
		Name: "final",
		Run: finalSubcommand,
		Description: "Render the final video",
	},
	{
		Name: "inspect",
		Run: inspectSubcommand,
		Description: "Inspect markers in the CSV file",
	},
}

func usage() {
	fmt.Printf("Usage: markut <SUBCOMMAND> [OPTIONS]\n")
	fmt.Printf("SUBCOMMANDS:\n")
	for _, subcommand := range Subcommands {
		fmt.Printf("    %s - %s\n", subcommand.Name, subcommand.Description);
	}
	fmt.Printf("ENVARS:\n")
	fmt.Printf("    FFMPEG_PREFIX      Prefix path for a custom ffmpeg distribution\n")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		fmt.Printf("ERROR: No subcommand is provided\n")
		os.Exit(1)
	}

	for _, subcommand := range Subcommands {
		if subcommand.Name == os.Args[1] {
			err := subcommand.Run(os.Args[2:])
			if err != nil {
				fmt.Printf("ERROR: %s\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

	usage()
	fmt.Printf("ERROR: Unknown subcommand %s\n", os.Args[1])
	os.Exit(1)
}
