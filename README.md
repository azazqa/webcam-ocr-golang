# webcam-ocr

USB 웹캠으로 모니터 문서 화면 전환을 감지해 PNG를 저장하고(작업 A), 이어서 OCR로 텍스트를 추출하는(작업 B) Windows용 프로그램입니다.

현재 저장소에는 **기반 패키지 + 작업 A(`internal/capture`)** 까지 구현되어 있습니다. 통합 GUI는 다음 단계입니다.

## 요구 사항

- **Go 1.27.4** (`go.mod`: `go 1.27`)
- 실행 대상: Windows 11 (웹캠·Windows OCR)
- 개발: WSL2에서 `CGO_ENABLED=0` 크로스 빌드

## 패키지

| 패키지 | 역할 |
|--------|------|
| `internal/camera` | Media Foundation 웹캠 캡처 (Windows) |
| `internal/imgconv` | MJPG/YUY2/NV12/RGB → image, PNG(`BestSpeed`) |
| `internal/extract` | 화면 전환 감지 (`StableDuration` / `Lookback`) |
| `internal/capture` | 작업 A 세션 (감지 → PNG / `capture_log.csv` / `session.json`) |
| `internal/ocr` | Windows OCR · Tesseract 엔진 |
| `internal/config` | `%APPDATA%\webcam-ocr\config.json` |

`prototype/` 은 참고용 시제품이며 gitignore됩니다. 본 트리는 시제품을 통째로 이식하지 않았습니다.

## 빌드 / 테스트

```bash
go version   # go1.27.4
./build.sh test
./build.sh race
./build.sh          # windows/amd64 패키지 스모크
```

## Windows 실기 체크리스트 (GUI 연동 후)

1. 설정 > 개인 정보 및 보안 > 카메라 > 데스크톱 앱 카메라 허용
2. 한국어 OCR 언어 팩 설치 여부 확인
3. 카메라 목록·미리보기
4. 고정 설치 웹캠으로 화면 전환 감지·PNG 세션 저장
5. 수동 캡처·무변화 자동 종료

## 라이선스 참고

`third_party/windows-camera-go` 는 업스트림 MIT 라이선스 수정본입니다. 자세한 내용은 `third_party/windows-camera-go/PATCHED.md` 를 보세요.
