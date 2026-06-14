package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
)

const alg = "aes-256-gcm"
const legacyAlg = "chacha20-hmac-sha256"

var legacyInfo = []byte("murat encrypted json v1")
var legacyMACPrefix = []byte("MURAT1")

type envelope struct {
	Version int    `json:"v"`
	Alg     string `json:"alg"`
	Salt    string `json:"salt,omitempty"`
	Nonce   string `json:"nonce"`
	CT      string `json:"ct"`
	MAC     string `json:"mac,omitempty"`
}

type Box struct {
	aead cipher.AEAD
	key  []byte
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		sum := sha256.Sum256(key)
		key = sum[:]
	}
	storedKey := append([]byte(nil), key...)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, key: storedKey}, nil
}

func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	material := hkdfSHA256(b.key, salt, legacyInfo, 64)
	encKey, macKey := material[:32], material[32:]
	ct := chacha20XOR(encKey, nonce, plaintext)
	mac := hmac.New(sha256.New, macKey)
	mac.Write(legacyMACPrefix)
	mac.Write(salt)
	mac.Write(nonce)
	mac.Write(ct)
	env := envelope{
		Version: 1,
		Alg:     legacyAlg,
		Salt:    base64.RawURLEncoding.EncodeToString(salt),
		Nonce:   base64.RawURLEncoding.EncodeToString(nonce),
		CT:      base64.RawURLEncoding.EncodeToString(ct),
		MAC:     base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
	}
	return json.Marshal(env)
}

func (b *Box) Open(data []byte) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	if env.Version != 1 {
		return nil, fmt.Errorf("unsupported encrypted blob")
	}
	if env.Alg == legacyAlg {
		return b.openLegacy(env)
	}
	if env.Alg != alg {
		return nil, fmt.Errorf("unsupported encrypted blob")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, err
	}
	ct, err := base64.RawURLEncoding.DecodeString(env.CT)
	if err != nil {
		return nil, err
	}
	return b.aead.Open(nil, nonce, ct, nil)
}

func (b *Box) openLegacy(env envelope) ([]byte, error) {
	salt, err := b64(env.Salt)
	if err != nil {
		return nil, err
	}
	nonce, err := b64(env.Nonce)
	if err != nil {
		return nil, err
	}
	if len(nonce) != 12 || len(salt) != 16 {
		return nil, fmt.Errorf("encrypted file is corrupt")
	}
	ct, err := b64(env.CT)
	if err != nil {
		return nil, err
	}
	expected, err := b64(env.MAC)
	if err != nil {
		return nil, err
	}
	material := hkdfSHA256(b.key, salt, legacyInfo, 64)
	encKey, macKey := material[:32], material[32:]
	mac := hmac.New(sha256.New, macKey)
	mac.Write(legacyMACPrefix)
	mac.Write(salt)
	mac.Write(nonce)
	mac.Write(ct)
	if !hmac.Equal(mac.Sum(nil), expected) {
		return nil, fmt.Errorf("encrypted file authentication failed")
	}
	return chacha20XOR(encKey, nonce, ct), nil
}

func b64(value string) ([]byte, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return data, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func hkdfSHA256(secret, salt, info []byte, length int) []byte {
	prkMac := hmac.New(sha256.New, salt)
	prkMac.Write(secret)
	prk := prkMac.Sum(nil)
	out := []byte{}
	block := []byte{}
	for counter := byte(1); len(out) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(block)
		mac.Write(info)
		mac.Write([]byte{counter})
		block = mac.Sum(nil)
		out = append(out, block...)
	}
	return out[:length]
}

func chacha20XOR(key, nonce, data []byte) []byte {
	out := make([]byte, len(data))
	counter := uint32(1)
	for offset := 0; offset < len(data); offset += 64 {
		block := chacha20Block(key, counter, nonce)
		counter++
		for i := 0; i < 64 && offset+i < len(data); i++ {
			out[offset+i] = data[offset+i] ^ block[i]
		}
	}
	return out
}

func chacha20Block(key []byte, counter uint32, nonce []byte) [64]byte {
	constants := []byte("expand 32-byte k")
	state := [16]uint32{
		binary.LittleEndian.Uint32(constants[0:4]),
		binary.LittleEndian.Uint32(constants[4:8]),
		binary.LittleEndian.Uint32(constants[8:12]),
		binary.LittleEndian.Uint32(constants[12:16]),
		binary.LittleEndian.Uint32(key[0:4]),
		binary.LittleEndian.Uint32(key[4:8]),
		binary.LittleEndian.Uint32(key[8:12]),
		binary.LittleEndian.Uint32(key[12:16]),
		binary.LittleEndian.Uint32(key[16:20]),
		binary.LittleEndian.Uint32(key[20:24]),
		binary.LittleEndian.Uint32(key[24:28]),
		binary.LittleEndian.Uint32(key[28:32]),
		counter,
		binary.LittleEndian.Uint32(nonce[0:4]),
		binary.LittleEndian.Uint32(nonce[4:8]),
		binary.LittleEndian.Uint32(nonce[8:12]),
	}
	working := state
	for i := 0; i < 10; i++ {
		quarterRound(&working, 0, 4, 8, 12)
		quarterRound(&working, 1, 5, 9, 13)
		quarterRound(&working, 2, 6, 10, 14)
		quarterRound(&working, 3, 7, 11, 15)
		quarterRound(&working, 0, 5, 10, 15)
		quarterRound(&working, 1, 6, 11, 12)
		quarterRound(&working, 2, 7, 8, 13)
		quarterRound(&working, 3, 4, 9, 14)
	}
	var out [64]byte
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(out[i*4:], working[i]+state[i])
	}
	return out
}

func quarterRound(state *[16]uint32, a, b, c, d int) {
	state[a] += state[b]
	state[d] = bits.RotateLeft32(state[d]^state[a], 16)
	state[c] += state[d]
	state[b] = bits.RotateLeft32(state[b]^state[c], 12)
	state[a] += state[b]
	state[d] = bits.RotateLeft32(state[d]^state[a], 8)
	state[c] += state[d]
	state[b] = bits.RotateLeft32(state[b]^state[c], 7)
}
