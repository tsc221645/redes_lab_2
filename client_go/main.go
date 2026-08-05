package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	host            = "127.0.0.1"
	port            = 1025
	maxMessageBytes = 1_000_000
	crc32Polynomial = "100000100110000010001110110110111"
	resultsFile     = "resultados.csv"
)

type Request struct {
	Action string      `json:"action"`
	Data   RequestData `json:"data"`
}

type RequestData struct {
	Algorithm         string `json:"algorithm"`
	Frame             string `json:"frame"`
	OriginalBitLength int    `json:"original_bit_length,omitempty"`
}

type Response struct {
	Action string         `json:"action"`
	Data   map[string]any `json:"data"`
}

type NoiseResult struct {
	Bits             string
	FlippedPositions []int
}

type TrialResult struct {
	Timestamp          string
	Algorithm          string
	Message            string
	MessageLength      int
	OriginalBits       int
	EncodedBits        int
	OverheadBits       int
	OverheadPercentage float64
	ErrorMode          string
	ErrorProbability   float64
	ForcedBit          int
	FlippedBits        int
	Status             string
	MessageRecovered   bool
	ElapsedMS          float64
}

func textToBits(text string) (string, error) {
	for _, r := range text {
		if r > 127 {
			return "", errors.New("el mensaje debe contener únicamente caracteres ASCII")
		}
	}

	var builder strings.Builder
	builder.Grow(len(text) * 8)

	for i := 0; i < len(text); i++ {
		builder.WriteString(fmt.Sprintf("%08b", text[i]))
	}

	return builder.String(), nil
}

func validateBits(bits string) error {
	if bits == "" {
		return errors.New("la cadena binaria no puede estar vacía")
	}

	for _, bit := range bits {
		if bit != '0' && bit != '1' {
			return errors.New("la cadena solo puede contener 0 y 1")
		}
	}

	return nil
}

func isParityPosition(position int) bool {
	return position > 0 && (position&(position-1)) == 0
}

func requiredParityBits(dataBitCount int) int {
	r := 0
	for (1 << r) < dataBitCount+r+1 {
		r++
	}
	return r
}

func hammingEncode(dataBits string) (string, error) {
	if err := validateBits(dataBits); err != nil {
		return "", err
	}

	parityBits := requiredParityBits(len(dataBits))
	totalBits := len(dataBits) + parityBits
	codeword := make([]byte, totalBits+1)

	dataIndex := 0
	for position := 1; position <= totalBits; position++ {
		if isParityPosition(position) {
			codeword[position] = '0'
		} else {
			codeword[position] = dataBits[dataIndex]
			dataIndex++
		}
	}

	for parityPosition := 1; parityPosition <= totalBits; parityPosition <<= 1 {
		var parity byte
		for position := 1; position <= totalBits; position++ {
			if position&parityPosition != 0 && position != parityPosition {
				parity ^= codeword[position] - '0'
			}
		}
		codeword[parityPosition] = parity + '0'
	}

	return string(codeword[1:]), nil
}

func crc32Remainder(dataBits string) (string, error) {
	if err := validateBits(dataBits); err != nil {
		return "", err
	}

	dividend := []byte(dataBits + strings.Repeat("0", 32))
	generator := []byte(crc32Polynomial)

	for index := 0; index < len(dataBits); index++ {
		if dividend[index] == '1' {
			for offset, generatorBit := range generator {
				left := dividend[index+offset] - '0'
				right := generatorBit - '0'
				dividend[index+offset] = (left ^ right) + '0'
			}
		}
	}

	return string(dividend[len(dividend)-32:]), nil
}

func crc32Encode(originalDataBits string) (string, int, error) {
	if err := validateBits(originalDataBits); err != nil {
		return "", 0, err
	}

	originalLength := len(originalDataBits)
	dataBits := originalDataBits

	if len(dataBits) <= 32 {
		dataBits += strings.Repeat("0", 33-len(dataBits))
	}

	remainder, err := crc32Remainder(dataBits)
	if err != nil {
		return "", 0, err
	}

	return dataBits + remainder, originalLength, nil
}

func applyRandomNoise(bits string, probability float64, rng *rand.Rand) (NoiseResult, error) {
	if err := validateBits(bits); err != nil {
		return NoiseResult{}, err
	}
	if probability < 0 || probability > 1 {
		return NoiseResult{}, errors.New("la probabilidad debe estar entre 0 y 1")
	}

	output := []byte(bits)
	flipped := make([]int, 0)

	for index := range output {
		if rng.Float64() < probability {
			if output[index] == '0' {
				output[index] = '1'
			} else {
				output[index] = '0'
			}
			flipped = append(flipped, index+1)
		}
	}

	return NoiseResult{Bits: string(output), FlippedPositions: flipped}, nil
}

func applyForcedFlip(bits string, bitPosition int) (NoiseResult, error) {
	if err := validateBits(bits); err != nil {
		return NoiseResult{}, err
	}
	if bitPosition < 1 || bitPosition > len(bits) {
		return NoiseResult{}, fmt.Errorf("la posición debe estar entre 1 y %d", len(bits))
	}

	output := []byte(bits)
	index := bitPosition - 1
	if output[index] == '0' {
		output[index] = '1'
	} else {
		output[index] = '0'
	}

	return NoiseResult{
		Bits:             string(output),
		FlippedPositions: []int{bitPosition},
	}, nil
}

func encodeMessage(message, algorithm string) (string, int, int, error) {
	dataBits, err := textToBits(message)
	if err != nil {
		return "", 0, 0, err
	}

	switch strings.ToUpper(algorithm) {
	case "HAMMING":
		frame, err := hammingEncode(dataBits)
		return frame, len(dataBits), len(dataBits), err
	case "CRC32":
		frame, originalLength, err := crc32Encode(dataBits)
		return frame, len(dataBits), originalLength, err
	default:
		return "", 0, 0, errors.New("algoritmo no soportado")
	}
}

func buildRequest(message, algorithm, errorMode string, probability float64, forcedBit int, rng *rand.Rand) (Request, NoiseResult, int, int, error) {
	frame, originalBits, originalBitLength, err := encodeMessage(message, algorithm)
	if err != nil {
		return Request{}, NoiseResult{}, 0, 0, err
	}

	var noise NoiseResult

	switch errorMode {
	case "none":
		noise = NoiseResult{Bits: frame}
	case "random":
		noise, err = applyRandomNoise(frame, probability, rng)
	case "forced":
		noise, err = applyForcedFlip(frame, forcedBit)
	default:
		err = errors.New("modo de error desconocido")
	}

	if err != nil {
		return Request{}, NoiseResult{}, 0, 0, err
	}

	data := RequestData{
		Algorithm: strings.ToUpper(algorithm),
		Frame:     noise.Bits,
	}

	if strings.EqualFold(algorithm, "CRC32") {
		data.OriginalBitLength = originalBitLength
	}

	return Request{
		Action: "send_frame",
		Data:   data,
	}, noise, originalBits, len(frame), nil
}

func sendRequest(writer *bufio.Writer, request Request) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(encoded) > maxMessageBytes {
		return errors.New("la solicitud supera el tamaño máximo")
	}

	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return writer.Flush()
}

func receiveResponse(reader *bufio.Reader) (Response, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Response{}, errors.New("el servidor cerró la conexión")
		}
		return Response{}, err
	}

	var response Response
	if err := json.Unmarshal(line, &response); err != nil {
		return Response{}, err
	}
	return response, nil
}

func valueAsString(data map[string]any, key string) string {
	value, exists := data[key]
	if !exists || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func appendCSV(result TrialResult) error {
	fileExists := false
	if info, err := os.Stat(resultsFile); err == nil && info.Size() > 0 {
		fileExists = true
	}

	file, err := os.OpenFile(resultsFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if !fileExists {
		header := []string{
			"timestamp", "algorithm", "message", "message_length_chars",
			"original_bits", "encoded_bits", "overhead_bits",
			"overhead_percentage", "error_mode", "error_probability",
			"forced_bit", "flipped_bits", "status",
			"message_recovered", "elapsed_ms",
		}
		if err := writer.Write(header); err != nil {
			return err
		}
	}

	record := []string{
		result.Timestamp,
		result.Algorithm,
		result.Message,
		strconv.Itoa(result.MessageLength),
		strconv.Itoa(result.OriginalBits),
		strconv.Itoa(result.EncodedBits),
		strconv.Itoa(result.OverheadBits),
		fmt.Sprintf("%.4f", result.OverheadPercentage),
		result.ErrorMode,
		fmt.Sprintf("%.6f", result.ErrorProbability),
		strconv.Itoa(result.ForcedBit),
		strconv.Itoa(result.FlippedBits),
		result.Status,
		strconv.FormatBool(result.MessageRecovered),
		fmt.Sprintf("%.3f", result.ElapsedMS),
	}

	return writer.Write(record)
}

func executeTrial(
	socketReader *bufio.Reader,
	socketWriter *bufio.Writer,
	message, algorithm, errorMode string,
	probability float64,
	forcedBit int,
	rng *rand.Rand,
	showDetails bool,
) (TrialResult, error) {
	request, noise, originalBits, encodedBits, err := buildRequest(
		message, algorithm, errorMode, probability, forcedBit, rng,
	)
	if err != nil {
		return TrialResult{}, err
	}

	start := time.Now()
	if err := sendRequest(socketWriter, request); err != nil {
		return TrialResult{}, err
	}

	response, err := receiveResponse(socketReader)
	if err != nil {
		return TrialResult{}, err
	}
	elapsed := time.Since(start)

	status := valueAsString(response.Data, "status")
	recoveredMessage := valueAsString(response.Data, "message")
	messageRecovered := recoveredMessage == message

	overheadBits := encodedBits - originalBits
	overheadPercentage := 0.0
	if originalBits > 0 {
		overheadPercentage = float64(overheadBits) / float64(originalBits) * 100
	}

	result := TrialResult{
		Timestamp:          time.Now().Format(time.RFC3339),
		Algorithm:          strings.ToUpper(algorithm),
		Message:            message,
		MessageLength:      len(message),
		OriginalBits:       originalBits,
		EncodedBits:        encodedBits,
		OverheadBits:       overheadBits,
		OverheadPercentage: overheadPercentage,
		ErrorMode:          errorMode,
		ErrorProbability:   probability,
		ForcedBit:          forcedBit,
		FlippedBits:        len(noise.FlippedPositions),
		Status:             status,
		MessageRecovered:   messageRecovered,
		ElapsedMS:          float64(elapsed.Microseconds()) / 1000.0,
	}

	if err := appendCSV(result); err != nil {
		return TrialResult{}, err
	}

	if showDetails {
		fmt.Printf("\n[CLIENT] Algoritmo: %s\n", result.Algorithm)
		fmt.Printf("[PRESENTACIÓN] Bits originales: %d\n", result.OriginalBits)
		fmt.Printf("[ENLACE] Bits codificados: %d\n", result.EncodedBits)
		fmt.Printf("[ENLACE] Overhead: %d bits (%.2f%%)\n", result.OverheadBits, result.OverheadPercentage)
		fmt.Printf("[RUIDO] Bits alterados: %d\n", result.FlippedBits)
		if len(noise.FlippedPositions) > 0 {
			fmt.Printf("[RUIDO] Posiciones: %v\n", noise.FlippedPositions)
		}
		fmt.Printf("[SERVER] Estado: %s\n", status)
		fmt.Printf("[SERVER] Detalle: %s\n", valueAsString(response.Data, "detail"))
		if recoveredMessage != "" {
			fmt.Printf("[SERVER] Mensaje recuperado: %s\n", recoveredMessage)
		}
		fmt.Printf("[RESULTADO] Mensaje correcto: %t\n", messageRecovered)
		fmt.Printf("[RESULTADO] Tiempo: %.3f ms\n", result.ElapsedMS)
		fmt.Printf("[CSV] Guardado en %s\n", resultsFile)
	}

	return result, nil
}

func runManual(reader *bufio.Reader, socketReader *bufio.Reader, socketWriter *bufio.Writer, rng *rand.Rand) {
	fmt.Print("Mensaje ASCII: ")
	message, _ := reader.ReadString('\n')
	message = strings.TrimSpace(message)

	fmt.Print("Algoritmo [HAMMING/CRC32]: ")
	algorithm, _ := reader.ReadString('\n')
	algorithm = strings.ToUpper(strings.TrimSpace(algorithm))

	fmt.Println("Modo de error:")
	fmt.Println("1) Sin error")
	fmt.Println("2) Error aleatorio")
	fmt.Println("3) Cambiar exactamente un bit")
	fmt.Print("Seleccione: ")
	modeValue, _ := reader.ReadString('\n')
	modeValue = strings.TrimSpace(modeValue)

	errorMode := "none"
	probability := 0.0
	forcedBit := 0

	switch modeValue {
	case "2":
		errorMode = "random"
		fmt.Print("Probabilidad [0-1]: ")
		raw, _ := reader.ReadString('\n')
		probability, _ = strconv.ParseFloat(strings.TrimSpace(raw), 64)
	case "3":
		errorMode = "forced"
		fmt.Print("Posición del bit, comenzando en 1: ")
		raw, _ := reader.ReadString('\n')
		forcedBit, _ = strconv.Atoi(strings.TrimSpace(raw))
	}

	_, err := executeTrial(
		socketReader, socketWriter,
		message, algorithm, errorMode,
		probability, forcedBit, rng, true,
	)
	if err != nil {
		fmt.Printf("[ERROR] %v\n", err)
	}
}

func repeatedMessage(size int) string {
	return strings.Repeat("A", size)
}

func runAutomatedTests(socketReader *bufio.Reader, socketWriter *bufio.Writer, rng *rand.Rand) {
	sizes := []int{8, 32, 128, 512}
	probabilities := []float64{0, 0.0001, 0.001, 0.005, 0.01}
	algorithms := []string{"HAMMING", "CRC32"}
	repetitions := 20

	total := len(sizes) * len(probabilities) * len(algorithms) * repetitions
	current := 0

	fmt.Printf("[TESTS] Ejecutando %d transmisiones...\n", total)

	for _, algorithm := range algorithms {
		for _, size := range sizes {
			message := repeatedMessage(size)

			for _, probability := range probabilities {
				for trial := 1; trial <= repetitions; trial++ {
					current++

					_, err := executeTrial(
						socketReader, socketWriter,
						message, algorithm, "random",
						probability, 0, rng, false,
					)
					if err != nil {
						fmt.Printf("\n[ERROR] Prueba %d: %v\n", current, err)
						return
					}

					if current%50 == 0 || current == total {
						fmt.Printf("\r[TESTS] Progreso: %d/%d", current, total)
					}
				}
			}
		}
	}

	fmt.Printf("\n[TESTS] Finalizadas. Resultados en %s\n", resultsFile)
}

func main() {
	address := fmt.Sprintf("%s:%d", host, port)
	connection, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No fue posible conectar con %s: %v\n", address, err)
		os.Exit(1)
	}
	defer connection.Close()

	consoleReader := bufio.NewReader(os.Stdin)
	socketReader := bufio.NewReader(connection)
	socketWriter := bufio.NewWriter(connection)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	fmt.Printf("[CLIENT] Conectado a %s\n", address)

	for {
		fmt.Println("\n=== LABORATORIO 2 ===")
		fmt.Println("1) Prueba manual")
		fmt.Println("2) Ejecutar matriz automática")
		fmt.Println("3) Salir")
		fmt.Print("Seleccione: ")

		option, err := consoleReader.ReadString('\n')
		if err != nil {
			fmt.Printf("[ERROR] %v\n", err)
			return
		}

		switch strings.TrimSpace(option) {
		case "1":
			runManual(consoleReader, socketReader, socketWriter, rng)
		case "2":
			runAutomatedTests(socketReader, socketWriter, rng)
		case "3":
			return
		default:
			fmt.Println("Opción inválida.")
		}
	}
}
