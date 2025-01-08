package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	if err := r.ParseForm(); err != nil {
		respondWithError(w, http.StatusBadRequest, "Exceded size limit", err)
		return
	}

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to retrieve the video metadata", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized user", nil)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to get video", err)
		return
	}
	defer file.Close()

	mediatype, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}
	if mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create the temp file", err)
		return
	}
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to copy file", err)
		return

	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to reset pointer", err)
		return
	}

	fsTempFile, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to process fro fast start", err)
		return
	}

	newTempFile, err := os.Open(fsTempFile)
	defer os.Remove(newTempFile.Name())
	defer newTempFile.Close()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to open new temp file", err)
		return
	}

	randBytes := make([]byte, 32)
	_, err = rand.Read(randBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to populate with random bytes", err)
		return
	}
	randStr := base64.RawURLEncoding.EncodeToString(randBytes)
	if randStr == "" {
		respondWithError(w, http.StatusInternalServerError, "Unable to create random string", err)
		return
	}

	key := randStr + ".mp4"

	ar, err := getVideoAspectRatio(newTempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get Aspect Ratio", err)
		return
	}

	if ar == "16:9" {
		key = "landscape/" + key
	}
	if ar == "9:16" {
		key = "portrait/" + key
	}
	if ar == "other" {
		key = "other/" + key
	}

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        newTempFile,
		ContentType: &mediatype,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to put object", err)
		return
	}
	url := cfg.s3CfDistribution + "/" + key
	video.VideoURL = &url
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting signed video", err)
		return
	}
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func getVideoAspectRatio(filePath string) (string, error) {
	type video struct {
		Streams []struct {
			Width  int `json:"width,omitempty"`
			Height int `json:"height,omitempty"`
		} `json:"streams"`
	}

	command := "ffprobe"
	arg0 := "-v"
	arg1 := "error"
	arg2 := "-print_format"
	arg3 := "json"
	arg4 := "-show_streams"
	arg5 := filePath
	cmd := exec.Command(command, arg0, arg1, arg2, arg3, arg4, arg5)

	var buffer bytes.Buffer

	cmd.Stdout = &buffer
	err := cmd.Run()
	if err != nil {
		return "", errors.New("error running ffprobe command")
	}

	var vidSize video
	err = json.Unmarshal(buffer.Bytes(), &vidSize)
	if err != nil {
		return "", errors.New("error unmarshalling in aspect ratio")
	}

	var ratio string
	w := vidSize.Streams[0].Width
	h := vidSize.Streams[0].Height

	if ar(w, h, 9) && ar(h, w, 16) {
		ratio = "9:16"
	} else if ar(w, h, 16) && ar(h, w, 9) {
		ratio = "16:9"
	} else {
		ratio = "other"
	}

	return ratio, nil
}

func processVideoForFastStart(filePath string) (string, error) {
	newFilePath := filePath + ".processing"

	command := "ffmpeg"
	arg0 := "-i"
	arg1 := filePath
	arg2 := "-c"
	arg3 := "copy"
	arg4 := "-movflags"
	arg5 := "faststart"
	arg6 := "-f"
	arg7 := "mp4"
	arg8 := newFilePath
	cmd := exec.Command(command, arg0, arg1, arg2, arg3, arg4, arg5, arg6, arg7, arg8)
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return newFilePath, nil
}
