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

type Envelope struct {
	Action string       `json:"action"`
	Data   EnvelopeData `json:"data"`
}
type EnvelopeData struct {
	Algorithm         string         `json:"algorithm"`
	Frame             string         `json:"frame"`
	OriginalBitLength int            `json:"original_bit_length"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}
type NoiseResult struct {
	Bits             string
	FlippedPositions []int
}
type TrialResult struct {
	Timestamp, Algorithm, Message, ErrorMode, RequestStatus, ResponseStatus                  string
	MessageLength, OriginalBits, EncodedBits, OverheadBits, FlippedBits, ResponseFlippedBits int
	OverheadPercentage, ErrorProbability, ElapsedMS                                          float64
	ForcedBit                                                                                int
	MessageRecovered, RequestRecovered, ResponseRecovered                                    bool
}

func validateBits(bits string) error {
	if bits == "" {
		return errors.New("la cadena binaria no puede estar vacia")
	}
	for _, b := range bits {
		if b != '0' && b != '1' {
			return errors.New("la cadena solo puede contener 0 y 1")
		}
	}
	return nil
}
func textToBits(text string) (string, error) {
	for _, r := range text {
		if r > 127 {
			return "", errors.New("el mensaje debe contener unicamente ASCII")
		}
	}
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		fmt.Fprintf(&b, "%08b", text[i])
	}
	return b.String(), nil
}
func bitsToText(bits string) (string, error) {
	if err := validateBits(bits); err != nil {
		return "", err
	}
	if len(bits)%8 != 0 {
		return "", errors.New("los bits recuperados no forman bytes")
	}
	var b strings.Builder
	for i := 0; i < len(bits); i += 8 {
		b.WriteByte(byte(parseBinary(bits[i : i+8])))
	}
	return b.String(), nil
}
func parseBinary(s string) int {
	n := 0
	for _, c := range s {
		n = n*2 + int(c-'0')
	}
	return n
}
func parityPosition(p int) bool { return p > 0 && p&(p-1) == 0 }

// Hamming(12,8): posiciones 1,2,4,8 son paridad; todo se numera de izquierda a derecha.
func hammingEncode(data string) (string, error) {
	if err := validateBits(data); err != nil {
		return "", err
	}
	if len(data)%8 != 0 {
		return "", errors.New("Hamming(12,8) requiere bytes")
	}
	var out strings.Builder
	for s := 0; s < len(data); s += 8 {
		w := make([]byte, 13)
		j := 0
		for p := 1; p <= 12; p++ {
			if !parityPosition(p) {
				w[p] = data[s+j]
				j++
			}
		}
		for _, pp := range []int{1, 2, 4, 8} {
			parity := byte(0)
			for p := 1; p <= 12; p++ {
				if p&pp != 0 && p != pp {
					parity ^= w[p] - '0'
				}
			}
			w[pp] = parity + '0'
		}
		for p := 1; p <= 12; p++ {
			out.WriteByte(w[p])
		}
	}
	return out.String(), nil
}
func hammingDecode(frame string) (string, []int, error) {
	if err := validateBits(frame); err != nil {
		return "", nil, err
	}
	if len(frame)%12 != 0 {
		return "", nil, errors.New("trama Hamming invalida")
	}
	var data strings.Builder
	corrected := []int{}
	for s := 0; s < len(frame); s += 12 {
		w := make([]byte, 13)
		for i := 1; i <= 12; i++ {
			w[i] = frame[s+i-1]
		}
		syndrome := 0
		for _, pp := range []int{1, 2, 4, 8} {
			p := byte(0)
			for i := 1; i <= 12; i++ {
				if i&pp != 0 {
					p ^= w[i] - '0'
				}
			}
			if p != 0 {
				syndrome += pp
			}
		}
		if syndrome > 12 {
			return "", nil, errors.New("sindrome Hamming invalido")
		}
		if syndrome > 0 {
			w[syndrome] = byte('1' + ('0' - w[syndrome]))
			corrected = append(corrected, s+syndrome)
		}
		for i := 1; i <= 12; i++ {
			if !parityPosition(i) {
				data.WriteByte(w[i])
			}
		}
	}
	text, err := bitsToText(data.String())
	return text, corrected, err
}
func crcRemainder(data string) (string, error) {
	if err := validateBits(data); err != nil {
		return "", err
	}
	d := []byte(data + strings.Repeat("0", 32))
	for i := 0; i < len(data); i++ {
		if d[i] == '1' {
			for j, g := range crc32Polynomial {
				d[i+j] = byte((d[i+j] - '0') ^ byte(g-'0') + '0')
			}
		}
	}
	return string(d[len(d)-32:]), nil
}
func crcEncode(data string) (string, error) { r, e := crcRemainder(data); return data + r, e }
func crcDecode(frame string, original int) (string, error) {
	if err := validateBits(frame); err != nil {
		return "", err
	}
	if len(frame) <= 32 {
		return "", errors.New("trama CRC32 invalida")
	}
	data := frame[:len(frame)-32]
	r, _ := crcRemainder(data)
	if r != frame[len(frame)-32:] {
		return "", errors.New("CRC32 invalido")
	}
	if original <= 0 || original > len(data) || original%8 != 0 {
		return "", errors.New("longitud original invalida")
	}
	return bitsToText(data[:original])
}

func applyNoise(bits, mode string, probability float64, forced int, rng *rand.Rand) (NoiseResult, error) {
	if err := validateBits(bits); err != nil {
		return NoiseResult{}, err
	}
	out := []byte(bits)
	flips := []int{}
	if mode == "none" {
		return NoiseResult{bits, flips}, nil
	}
	if mode == "forced" {
		if forced < 1 || forced > len(out) {
			return NoiseResult{}, fmt.Errorf("bit forzado fuera de rango")
		}
		out[forced-1] = map[byte]byte{'0': '1', '1': '0'}[out[forced-1]]
		return NoiseResult{string(out), []int{forced}}, nil
	}
	if mode != "random" || probability < 0 || probability > 1 {
		return NoiseResult{}, errors.New("ruido invalido")
	}
	for i := range out {
		if rng.Float64() < probability {
			out[i] = map[byte]byte{'0': '1', '1': '0'}[out[i]]
			flips = append(flips, i+1)
		}
	}
	return NoiseResult{string(out), flips}, nil
}
func encodeMessage(message, algorithm string) (string, int, error) {
	bits, e := textToBits(message)
	if e != nil {
		return "", 0, e
	}
	var frame string
	if strings.EqualFold(algorithm, "HAMMING") {
		frame, e = hammingEncode(bits)
	} else if strings.EqualFold(algorithm, "CRC32") {
		frame, e = crcEncode(bits)
	} else {
		return "", 0, errors.New("algoritmo no soportado")
	}
	return frame, len(bits), e
}
func buildRequest(message, algorithm, mode string, prob float64, forced int, responseMode string, responseProb float64, responseForced int, rng *rand.Rand) (Envelope, NoiseResult, int, error) {
	frame, original, e := encodeMessage(message, algorithm)
	if e != nil {
		return Envelope{}, NoiseResult{}, 0, e
	}
	noise, e := applyNoise(frame, mode, prob, forced, rng)
	if e != nil {
		return Envelope{}, NoiseResult{}, 0, e
	}
	return Envelope{"send_frame", EnvelopeData{strings.ToUpper(algorithm), noise.Bits, original, map[string]any{"error_mode": mode, "error_probability": prob, "forced_bit": forced, "response_error_mode": responseMode, "response_error_probability": responseProb, "response_forced_bit": responseForced}}}, noise, original, e
}
func sendRequest(w *bufio.Writer, req Envelope) error {
	raw, e := json.Marshal(req)
	if e != nil {
		return e
	}
	if len(raw) > maxMessageBytes {
		return errors.New("solicitud demasiado grande")
	}
	_, e = w.Write(append(raw, '\n'))
	if e == nil {
		e = w.Flush()
	}
	return e
}
func receiveResponse(r *bufio.Reader) (Envelope, error) {
	line, e := r.ReadBytes('\n')
	if e != nil {
		if e == io.EOF {
			return Envelope{}, errors.New("servidor desconectado")
		}
		return Envelope{}, e
	}
	var x Envelope
	if e = json.Unmarshal(line, &x); e != nil {
		return x, e
	}
	return x, nil
}
func mapString(m map[string]any, k string) string {
	if v := m[k]; v != nil {
		return fmt.Sprint(v)
	}
	return ""
}
func decodeResponse(data EnvelopeData) (map[string]any, []int, error) {
	var msg string
	var corrections []int
	var e error
	data.Algorithm = strings.ToUpper(data.Algorithm)
	if data.Algorithm == "HAMMING" {
		msg, corrections, e = hammingDecode(data.Frame)
	} else if data.Algorithm == "CRC32" {
		msg, e = crcDecode(data.Frame, data.OriginalBitLength)
	} else {
		e = errors.New("algoritmo de respuesta no soportado")
	}
	if e != nil {
		return nil, corrections, e
	}
	var payload map[string]any
	if e = json.Unmarshal([]byte(msg), &payload); e != nil {
		return nil, corrections, e
	}
	return payload, corrections, nil
}

func appendCSV(x TrialResult) error {
	exists := false
	if s, e := os.Stat(resultsFile); e == nil && s.Size() > 0 {
		exists = true
	}
	f, e := os.OpenFile(resultsFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if e != nil {
		return e
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if !exists {
		w.Write([]string{"timestamp", "algorithm", "message", "message_length_chars", "original_bits", "encoded_bits", "overhead_bits", "overhead_percentage", "error_mode", "error_probability", "forced_bit", "flipped_bits", "status", "message_recovered", "elapsed_ms", "request_algorithm", "response_algorithm", "request_flipped_bits", "response_flipped_bits", "request_recovered", "response_recovered", "request_status", "response_status", "round_trip_ms"})
	}
	w.Write([]string{x.Timestamp, x.Algorithm, x.Message, strconv.Itoa(x.MessageLength), strconv.Itoa(x.OriginalBits), strconv.Itoa(x.EncodedBits), strconv.Itoa(x.OverheadBits), fmt.Sprintf("%.4f", x.OverheadPercentage), x.ErrorMode, fmt.Sprintf("%.6f", x.ErrorProbability), strconv.Itoa(x.ForcedBit), strconv.Itoa(x.FlippedBits), x.ResponseStatus, strconv.FormatBool(x.MessageRecovered), fmt.Sprintf("%.3f", x.ElapsedMS), x.Algorithm, x.Algorithm, strconv.Itoa(x.FlippedBits), strconv.Itoa(x.ResponseFlippedBits), strconv.FormatBool(x.RequestRecovered), strconv.FormatBool(x.ResponseRecovered), x.RequestStatus, x.ResponseStatus, fmt.Sprintf("%.3f", x.ElapsedMS)})
	w.Flush()
	return w.Error()
}

func executeTrial(r *bufio.Reader, w *bufio.Writer, message, algorithm, mode string, prob float64, forced int, responseMode string, responseProb float64, responseForced int, rng *rand.Rand, show bool) (TrialResult, error) {
	req, noise, original, e := buildRequest(message, algorithm, mode, prob, forced, responseMode, responseProb, responseForced, rng)
	if e != nil {
		return TrialResult{}, e
	}
	start := time.Now()
	if e = sendRequest(w, req); e != nil {
		return TrialResult{}, e
	}
	response, e := receiveResponse(r)
	if e != nil {
		return TrialResult{}, e
	}
	payload, corrections, e := decodeResponse(response.Data)
	elapsed := time.Since(start)
	if e != nil {
		return TrialResult{}, e
	}
	status := mapString(payload, "status")
	recovered := mapString(payload, "message") == message
	encoded := len(req.Data.Frame)
	x := TrialResult{Timestamp: time.Now().Format(time.RFC3339), Algorithm: strings.ToUpper(algorithm), Message: message, MessageLength: len(message), OriginalBits: original, EncodedBits: encoded, OverheadBits: encoded - original, ErrorMode: mode, ErrorProbability: prob, ForcedBit: forced, FlippedBits: len(noise.FlippedPositions), RequestStatus: mapString(payload, "request_status"), ResponseStatus: status, MessageRecovered: recovered, RequestRecovered: status != "error", ResponseRecovered: e == nil, ElapsedMS: float64(elapsed.Microseconds()) / 1000}
	if original > 0 {
		x.OverheadPercentage = float64(x.OverheadBits) / float64(original) * 100
	}
	if v := response.Data.Metadata["response_flipped_bits"]; v != nil {
		if a, ok := v.([]any); ok {
			x.ResponseFlippedBits = len(a)
		}
	}
	if e = appendCSV(x); e != nil {
		return x, e
	}
	if show {
		fmt.Printf("\n[CLIENT] Algoritmo: %s\n[SERVER] Estado: %s\n[SERVER] Mensaje: %s\n[ENLACE] Correcciones Hamming: %v\n[ENLACE] Overhead: %d bits (%.2f%%)\n[RUIDO] Bits alterados: %d\n[RESULTADO] Tiempo total: %.3f ms\n", x.Algorithm, status, mapString(payload, "message"), corrections, x.OverheadBits, x.OverheadPercentage, x.FlippedBits, x.ElapsedMS)
	}
	return x, nil
}

func runManual(in *bufio.Reader, sr *bufio.Reader, sw *bufio.Writer, rng *rand.Rand) {
	fmt.Print("Accion [login/withdraw/logout/mensaje]: ")
	a, _ := in.ReadString('\n')
	a = strings.TrimSpace(a)
	message := a
	if a == "login" {
		message = `{"action":"login","data":{"card":"221645","pin":"1234"}}`
	} else if a == "withdraw" {
		message = `{"action":"withdraw","data":{"amount":100}}`
	} else if a == "logout" {
		message = `{"action":"logout","data":{}}`
	}
	fmt.Print("Algoritmo [HAMMING/CRC32]: ")
	alg, _ := in.ReadString('\n')
	fmt.Print("Modo de error [none/random/forced]: ")
	mode, _ := in.ReadString('\n')
	mode = strings.TrimSpace(mode)
	p := 0.0
	forced := 0
	if mode == "random" {
		fmt.Print("Probabilidad: ")
		v, _ := in.ReadString('\n')
		p, _ = strconv.ParseFloat(strings.TrimSpace(v), 64)
	} else if mode == "forced" {
		fmt.Print("Bit: ")
		v, _ := in.ReadString('\n')
		forced, _ = strconv.Atoi(strings.TrimSpace(v))
	}
	fmt.Print("Ruido de respuesta [none/random/forced]: ")
	responseMode, _ := in.ReadString('\n')
	responseMode = strings.TrimSpace(responseMode)
	responseProb, responseForced := 0.0, 0
	if responseMode == "random" {
		fmt.Print("Probabilidad de respuesta: ")
		v, _ := in.ReadString('\n')
		responseProb, _ = strconv.ParseFloat(strings.TrimSpace(v), 64)
	} else if responseMode == "forced" {
		fmt.Print("Bit forzado en respuesta: ")
		v, _ := in.ReadString('\n')
		responseForced, _ = strconv.Atoi(strings.TrimSpace(v))
	}
	if _, e := executeTrial(sr, sw, message, strings.TrimSpace(alg), mode, p, forced, responseMode, responseProb, responseForced, rng, true); e != nil {
		fmt.Println("[ERROR]", e)
	}
}
func runAutomatedTests(sr *bufio.Reader, sw *bufio.Writer, rng *rand.Rand) {
	for _, alg := range []string{"HAMMING", "CRC32"} {
		for _, n := range []int{8, 32, 128, 512} {
			for _, p := range []float64{0, .0001, .001, .005, .01} {
				for i := 0; i < 20; i++ {
					if _, e := executeTrial(sr, sw, strings.Repeat("A", n), alg, "random", p, 0, "none", 0, 0, rng, false); e != nil {
						fmt.Println("[ERROR]", e)
						return
					}
				}
			}
		}
	}
	fmt.Println("[TESTS] Finalizadas.")
}
func main() {
	conn, e := net.Dial("tcp", fmt.Sprintf("%s:%d", host, port))
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return
	}
	defer conn.Close()
	in := bufio.NewReader(os.Stdin)
	sr := bufio.NewReader(conn)
	sw := bufio.NewWriter(conn)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	fmt.Println("[CLIENT] Conectado a 127.0.0.1:1025")
	for {
		fmt.Println("\n1) Prueba manual\n2) Matriz automatica\n3) Salir\nSeleccione: ")
		v, e := in.ReadString('\n')
		if e != nil {
			return
		}
		switch strings.TrimSpace(v) {
		case "1":
			runManual(in, sr, sw, rng)
		case "2":
			runAutomatedTests(sr, sw, rng)
		case "3":
			return
		default:
			fmt.Println("Opcion invalida")
		}
	}
}
