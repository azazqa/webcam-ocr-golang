module webcam-ocr

go 1.27

require (
	github.com/Kirizu-Official/windows-camera-go v0.2.0
	github.com/lxn/walk v0.0.0-20210112085537-c389da54e794
	github.com/lxn/win v0.0.0-20210218163916-a377121e959e
)

require (
	golang.org/x/sys v0.0.0-20201018230417-eeed37f84f13 // indirect
	gopkg.in/Knetic/govaluate.v3 v3.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/Kirizu-Official/windows-camera-go => ./third_party/windows-camera-go
