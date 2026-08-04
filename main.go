package main

import (
	"bufio"
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

func isParityPosition(position int) bool {
	return position > 0 && (position&(position-1)) == 0
}

func requiredParityBits(dataBitCount int) int {
	parityBits := 0
	for (1 << parityBits) < dataBitCount+parityBits+1 {
		parityBits++
	}
	return parityBits
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
			continue
		}
		codeword[position] = dataBits[dataIndex]
		dataIndex++
	}

	for parityPosition := 1; parityPosition <= totalBits; parityPosition <<= 1 {
		parity := byte(0)
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

	originalBitLength := len(originalDataBits)
	dataBits := originalDataBits
	if len(dataBits) <= 32 {
		dataBits += strings.Repeat("0", 33-len(dataBits))
	}

	remainder, err := crc32Remainder(dataBits)
	if err != nil {
		return "", 0, err
	}
	return dataBits + remainder, originalBitLength, nil
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

func applyNoise(bits string, probability float64, rng *rand.Rand) (NoiseResult, error) {
	if err := validateBits(bits); err != nil {
		return NoiseResult{}, err
	}
	if probability < 0 || probability > 1 {
		return NoiseResult{}, errors.New("la probabilidad debe estar entre 0 y 1")
	}

	output := []byte(bits)
	flippedPositions := make([]int, 0)
	for index := range output {
		if rng.Float64() < probability {
			if output[index] == '0' {
				output[index] = '1'
			} else {
				output[index] = '0'
			}
			flippedPositions = append(flippedPositions, index+1)
		}
	}

	return NoiseResult{Bits: string(output), FlippedPositions: flippedPositions}, nil
}

func buildRequest(message, algorithm string, errorProbability float64, rng *rand.Rand) (Request, NoiseResult, int, int, error) {
	dataBits, err := textToBits(message)
	if err != nil {
		return Request{}, NoiseResult{}, 0, 0, err
	}

	var frame string
	var originalBitLength int

	switch strings.ToUpper(algorithm) {
	case "HAMMING":
		frame, err = hammingEncode(dataBits)
		originalBitLength = len(dataBits)
	case "CRC32":
		frame, originalBitLength, err = crc32Encode(dataBits)
	default:
		return Request{}, NoiseResult{}, 0, 0, errors.New("algoritmo no soportado; use HAMMING o CRC32")
	}
	if err != nil {
		return Request{}, NoiseResult{}, 0, 0, err
	}

	noisyFrame, err := applyNoise(frame, errorProbability, rng)
	if err != nil {
		return Request{}, NoiseResult{}, 0, 0, err
	}

	data := RequestData{Algorithm: strings.ToUpper(algorithm), Frame: noisyFrame.Bits}
	if strings.EqualFold(algorithm, "CRC32") {
		data.OriginalBitLength = originalBitLength
	}

	return Request{Action: "send_frame", Data: data}, noisyFrame, len(dataBits), len(frame), nil
}

func sendRequest(writer *bufio.Writer, request Request) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("no se pudo serializar la solicitud: %w", err)
	}
	if len(encoded) > maxMessageBytes {
		return errors.New("la solicitud supera el tamaño máximo permitido")
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("no se pudo enviar la solicitud: %w", err)
	}
	return writer.Flush()
}

func receiveResponse(reader *bufio.Reader) (Response, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Response{}, errors.New("el servidor cerró la conexión")
		}
		return Response{}, fmt.Errorf("no se pudo leer la respuesta: %w", err)
	}
	if len(line) > maxMessageBytes {
		return Response{}, errors.New("la respuesta supera el tamaño máximo permitido")
	}

	var response Response
	if err := json.Unmarshal(line, &response); err != nil {
		return Response{}, fmt.Errorf("el servidor envió JSON inválido: %w", err)
	}
	return response, nil
}

func readAlgorithm(reader *bufio.Reader) (string, error) {
	for {
		fmt.Print("Algoritmo [HAMMING/CRC32]: ")
		value, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "HAMMING" || value == "CRC32" {
			return value, nil
		}
		fmt.Println("Opción inválida. Escriba HAMMING o CRC32.")
	}
}

func readProbability(reader *bufio.Reader) (float64, error) {
	for {
		fmt.Print("Probabilidad de error por bit [0.0 - 1.0]: ")
		value, err := reader.ReadString('\n')
		if err != nil {
			return 0, err
		}
		probability, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil && probability >= 0 && probability <= 1 {
			return probability, nil
		}
		fmt.Println("Ingrese un número entre 0 y 1, por ejemplo 0.01.")
	}
}

func printResponse(response Response) {
	fmt.Printf("\n[SERVER] Acción: %s\n", response.Action)
	status, _ := response.Data["status"].(string)
	detail, _ := response.Data["detail"].(string)
	message, _ := response.Data["message"].(string)

	fmt.Printf("[SERVER] Estado: %s\n", status)
	if detail != "" {
		fmt.Printf("[SERVER] Detalle: %s\n", detail)
	}
	if message != "" {
		fmt.Printf("[SERVER] Mensaje recuperado: %s\n", message)
	}
	if correctedBit, exists := response.Data["corrected_bit"]; exists {
		fmt.Printf("[SERVER] Bit corregido: %v\n", correctedBit)
	}
	if receivedCRC, exists := response.Data["received_crc"]; exists {
		fmt.Printf("[SERVER] CRC recibido: %v\n", receivedCRC)
	}
	if calculatedCRC, exists := response.Data["calculated_crc"]; exists {
		fmt.Printf("[SERVER] CRC calculado: %v\n", calculatedCRC)
	}
}

func main() {
	address := fmt.Sprintf("%s:%d", host, port)
	connection, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No fue posible conectar con %s: %v\n", address, err)
		os.Exit(1)
	}
	defer connection.Close()

	fmt.Printf("[CLIENT] Conectado a %s\n", address)
	fmt.Println("[CLIENT] Escriba 'salir' como mensaje para cerrar.")

	consoleReader := bufio.NewReader(os.Stdin)
	socketReader := bufio.NewReader(connection)
	socketWriter := bufio.NewWriter(connection)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		fmt.Print("\nMensaje ASCII: ")
		message, err := consoleReader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error leyendo el mensaje: %v\n", err)
			return
		}
		message = strings.TrimSpace(message)
		if strings.EqualFold(message, "salir") {
			fmt.Println("[CLIENT] Conexión finalizada.")
			return
		}
		if message == "" {
			fmt.Println("El mensaje no puede estar vacío.")
			continue
		}

		algorithm, err := readAlgorithm(consoleReader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error leyendo el algoritmo: %v\n", err)
			return
		}
		errorProbability, err := readProbability(consoleReader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error leyendo la probabilidad: %v\n", err)
			return
		}

		request, noise, originalBits, encodedBits, err := buildRequest(message, algorithm, errorProbability, rng)
		if err != nil {
			fmt.Printf("[CLIENT] No se pudo construir la trama: %v\n", err)
			continue
		}

		fmt.Printf("[PRESENTACIÓN] Bits originales: %d\n", originalBits)
		fmt.Printf("[ENLACE] Bits codificados: %d\n", encodedBits)
		fmt.Printf("[ENLACE] Overhead: %d bits\n", encodedBits-originalBits)
		fmt.Printf("[RUIDO] Bits alterados: %d\n", len(noise.FlippedPositions))
		if len(noise.FlippedPositions) > 0 {
			fmt.Printf("[RUIDO] Posiciones 1-based: %v\n", noise.FlippedPositions)
		}

		if err := sendRequest(socketWriter, request); err != nil {
			fmt.Fprintf(os.Stderr, "Error enviando la trama: %v\n", err)
			return
		}
		response, err := receiveResponse(socketReader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error recibiendo la respuesta: %v\n", err)
			return
		}
		printResponse(response)
	}
}
