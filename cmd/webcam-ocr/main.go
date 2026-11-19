//go:build windows

// webcam-ocr: Windows GUI for work A (image extraction) using internal/capture.
package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"webcam-ocr/internal/camera"
	"webcam-ocr/internal/capture"
	"webcam-ocr/internal/config"
	"webcam-ocr/internal/imgconv"
)

type gui struct {
	mw        *walk.MainWindow
	camCB     *walk.ComboBox
	fmtCB     *walk.ComboBox
	baseDirLE *walk.LineEdit
	autoRunCB *walk.CheckBox
	idleMinNE *walk.NumberEdit
	preview   *walk.CustomWidget
	startBtn  *walk.PushButton
	stopBtn   *walk.PushButton
	manualBtn *walk.PushButton
	openBtn   *walk.PushButton
	countLbl  *walk.Label
	idleLbl   *walk.Label
	status    *walk.StatusBarItem

	cfg      config.Config
	devices  []camera.Device
	cam      *camera.Camera
	suppress bool

	mu       sync.Mutex
	latest   image.Image
	frameBmp *walk.Bitmap // UI thread only
	frameW   int
	frameH   int
	pending  atomic.Bool

	previewStop chan struct{}
	previewDone chan struct{}

	sess   *capture.Session
	cancel context.CancelFunc
	busy   atomic.Bool
}

func main() {
	cfg, _ := config.Load()
	if cfg.LastBaseDir == "" {
		if exe, err := os.Executable(); err == nil {
			cfg.LastBaseDir = filepath.Join(filepath.Dir(exe), "captures")
		} else {
			cfg.LastBaseDir = "captures"
		}
	}
	idleMin := cfg.IdleDuration().Minutes()
	if idleMin < 1.0/6.0 {
		idleMin = 3
	}

	g := &gui{cfg: cfg}

	err := MainWindow{
		AssignTo: &g.mw,
		Title:    "Webcam OCR — 이미지 추출",
		Size:     Size{Width: 1100, Height: 700},
		MinSize:  Size{Width: 800, Height: 500},
		Font:     Font{Family: "Malgun Gothic", PointSize: 9},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: HBox{},
				Children: []Widget{
					Label{Text: "저장 위치"},
					LineEdit{AssignTo: &g.baseDirLE, Text: cfg.LastBaseDir, StretchFactor: 1},
					PushButton{Text: "찾아보기…", OnClicked: g.browseBase},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					Label{Text: "카메라"},
					ComboBox{AssignTo: &g.camCB, StretchFactor: 1, OnCurrentIndexChanged: g.onCamChanged},
					Label{Text: "해상도"},
					ComboBox{AssignTo: &g.fmtCB, StretchFactor: 1, OnCurrentIndexChanged: g.onFmtChanged},
					PushButton{Text: "새로고침", OnClicked: g.refreshCams},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					CheckBox{AssignTo: &g.autoRunCB, Text: "화면 변화가 없으면 자동 종료", Checked: cfg.AutoRunEnabled},
					Label{Text: "무변화(분)"},
					NumberEdit{AssignTo: &g.idleMinNE, Value: idleMin, MinValue: 0.2, MaxValue: 30, Decimals: 1},
					Label{AssignTo: &g.countLbl, Text: "저장 0장"},
					Label{AssignTo: &g.idleLbl, Text: ""},
					HSpacer{},
					PushButton{AssignTo: &g.startBtn, Text: "추출 시작", OnClicked: g.startExtract},
					PushButton{AssignTo: &g.stopBtn, Text: "추출 정지", Enabled: false, OnClicked: g.stopExtract},
					PushButton{AssignTo: &g.manualBtn, Text: "수동 캡처", Enabled: false, OnClicked: g.manualCapture},
					PushButton{AssignTo: &g.openBtn, Text: "결과 폴더", Enabled: false, OnClicked: g.openSession},
				},
			},
			// CustomWidget + Invalidate avoids ImageView.SetImage, which dismisses ComboBox dropdowns.
			CustomWidget{
				AssignTo:            &g.preview,
				StretchFactor:       1,
				ClearsBackground:    false,
				InvalidatesOnResize: true,
				PaintPixels:         g.paintPreview,
			},
		},
		StatusBarItems: []StatusBarItem{
			{AssignTo: &g.status, Text: "준비", Width: 800},
		},
	}.Create()
	if err != nil {
		panic(err)
	}
	g.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		g.stopPreview()
		g.stopExtract()
		g.saveCfg()
		if g.cam != nil {
			g.cam.Close()
			g.cam = nil
		}
		if g.frameBmp != nil {
			g.frameBmp.Dispose()
			g.frameBmp = nil
		}
	})
	g.refreshCams()
	g.mw.Run()
}

func comboDropped(cb *walk.ComboBox) bool {
	if cb == nil || cb.Handle() == 0 {
		return false
	}
	return win.SendMessage(cb.Handle(), win.CB_GETDROPPEDSTATE, 0, 0) != 0
}

func (g *gui) paintPreview(canvas *walk.Canvas, _ walk.Rectangle) error {
	cb := g.preview.ClientBoundsPixels()
	bg, err := walk.NewSolidColorBrush(walk.RGB(30, 30, 30))
	if err != nil {
		return err
	}
	defer bg.Dispose()
	_ = canvas.FillRectanglePixels(bg, cb)
	if g.frameBmp == nil || g.frameW <= 0 || g.frameH <= 0 {
		return nil
	}
	sw := float64(cb.Width) / float64(g.frameW)
	sh := float64(cb.Height) / float64(g.frameH)
	s := sw
	if sh < sw {
		s = sh
	}
	w, h := int(float64(g.frameW)*s), int(float64(g.frameH)*s)
	dst := walk.Rectangle{
		X: (cb.Width - w) / 2, Y: (cb.Height - h) / 2,
		Width: w, Height: h,
	}
	return canvas.DrawImageStretchedPixels(g.frameBmp, dst)
}

func (g *gui) setStatus(s string) {
	if g.status != nil {
		_ = g.status.SetText(s)
	}
}

func (g *gui) saveCfg() {
	g.cfg.LastBaseDir = g.baseDirLE.Text()
	g.cfg.AutoRunEnabled = g.autoRunCB.Checked()
	mins := g.idleMinNE.Value()
	g.cfg.IdleTimeout = (time.Duration(mins*60) * time.Second).String()
	_ = config.Save(g.cfg)
}

func (g *gui) browseBase() {
	dlg := new(walk.FileDialog)
	dlg.Title = "저장 위치 선택"
	dlg.FilePath = g.baseDirLE.Text()
	ok, err := dlg.ShowBrowseFolder(g.mw)
	if err != nil || !ok {
		return
	}
	_ = g.baseDirLE.SetText(dlg.FilePath)
}

func (g *gui) refreshCams() {
	g.stopPreview()
	if g.cam != nil {
		g.cam.Close()
		g.cam = nil
	}
	devs, err := camera.ListDevices()
	if err != nil {
		g.setStatus("카메라 목록 실패: " + err.Error())
		return
	}
	g.devices = devs
	names := make([]string, len(devs))
	for i, d := range devs {
		names[i] = fmt.Sprintf("%d: %s", d.Index, d.Name)
	}
	g.suppress = true
	_ = g.camCB.SetModel(names)
	g.suppress = false
	if len(names) == 0 {
		g.setStatus("카메라 없음")
		return
	}
	_ = g.camCB.SetCurrentIndex(0)
	g.onCamChanged()
}

func (g *gui) onCamChanged() {
	if g.suppress || g.busy.Load() {
		return
	}
	g.stopPreview()
	if g.cam != nil {
		g.cam.Close()
		g.cam = nil
	}
	i := g.camCB.CurrentIndex()
	if i < 0 || i >= len(g.devices) {
		return
	}
	cam, err := camera.Open(g.devices[i])
	if err != nil {
		g.setStatus(err.Error())
		return
	}
	g.cam = cam
	fs := cam.Formats()
	labels := make([]string, len(fs))
	for j, f := range fs {
		labels[j] = f.String()
	}
	g.suppress = true
	_ = g.fmtCB.SetModel(labels)
	best, ok := camera.BestFormat(fs)
	idx := 0
	if ok {
		for j, f := range fs {
			if f.Index == best.Index {
				idx = j
				break
			}
		}
	}
	_ = g.fmtCB.SetCurrentIndex(idx)
	g.suppress = false
	g.startPreview()
	g.setStatus("미리보기 중")
}

func (g *gui) onFmtChanged() {
	if g.suppress || g.busy.Load() || g.cam == nil {
		return
	}
	fi := g.fmtCB.CurrentIndex()
	info := g.cam.Info
	g.stopPreview()
	g.cam.Close()
	cam, err := camera.Open(info)
	if err != nil {
		g.cam = nil
		g.setStatus(err.Error())
		return
	}
	g.cam = cam
	fs := cam.Formats()
	labels := make([]string, len(fs))
	for j, f := range fs {
		labels[j] = f.String()
	}
	if fi < 0 || fi >= len(fs) {
		fi = 0
	}
	g.suppress = true
	_ = g.fmtCB.SetModel(labels)
	_ = g.fmtCB.SetCurrentIndex(fi)
	g.suppress = false
	g.startPreview()
}

func (g *gui) startPreview() {
	g.stopPreview()
	if g.cam == nil || g.busy.Load() {
		return
	}
	fs := g.cam.Formats()
	i := g.fmtCB.CurrentIndex()
	if i < 0 || i >= len(fs) {
		return
	}
	was := g.cam.Capturing()
	if err := g.cam.EnsureStart(fs[i]); err != nil {
		g.setStatus(err.Error())
		return
	}
	if !was {
		g.cam.Warmup(5)
	}
	g.previewStop = make(chan struct{})
	g.previewDone = make(chan struct{})
	go g.previewLoop()
	g.setStatus("미리보기 중")
}

func (g *gui) stopPreview() {
	if g.previewStop == nil {
		return
	}
	close(g.previewStop)
	<-g.previewDone
	g.previewStop, g.previewDone = nil, nil
}

func (g *gui) previewLoop() {
	defer close(g.previewDone)
	const uiMinInterval = 100 * time.Millisecond // ~10 fps
	var lastUI time.Time
	for {
		select {
		case <-g.previewStop:
			return
		default:
		}
		img, err := g.cam.Grab()
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		g.mu.Lock()
		g.latest = img
		g.mu.Unlock()

		now := time.Now()
		if !lastUI.IsZero() && now.Sub(lastUI) < uiMinInterval {
			continue
		}
		if g.pending.Swap(true) {
			continue
		}
		lastUI = now
		thumb := imgconv.Downscale(img, 960)
		b := thumb.Bounds()
		g.mw.Synchronize(func() {
			defer g.pending.Store(false)
			// Never touch widgets that steal focus while a ComboBox list is open.
			if comboDropped(g.camCB) || comboDropped(g.fmtCB) {
				return
			}
			bmp, err := walk.NewBitmapFromImageForDPI(thumb, 96)
			if err != nil {
				return
			}
			if g.frameBmp != nil {
				g.frameBmp.Dispose()
			}
			g.frameBmp = bmp
			g.frameW, g.frameH = b.Dx(), b.Dy()
			_ = g.preview.Invalidate()
		})
	}
}

func (g *gui) startExtract() {
	if g.busy.Load() || g.cam == nil {
		return
	}
	base := g.baseDirLE.Text()
	if base == "" {
		walk.MsgBox(g.mw, "안내", "저장 위치를 지정하세요.", walk.MsgBoxIconWarning)
		return
	}
	// Stop preview Grab only; keep MF capture running for capture.Session.
	g.stopPreview()

	idle := time.Duration(0)
	if g.autoRunCB.Checked() {
		idle = time.Duration(g.idleMinNE.Value()*60) * time.Second
		if idle < 10*time.Second {
			idle = 10 * time.Second
		}
	}

	fmtStr := ""
	fs := g.cam.Formats()
	i := g.fmtCB.CurrentIndex()
	if i >= 0 && i < len(fs) {
		fmtStr = fs[i].String()
		if err := g.cam.EnsureStart(fs[i]); err != nil {
			g.setStatus(err.Error())
			g.startPreview()
			return
		}
	} else {
		g.setStatus("해상도를 선택하세요")
		g.startPreview()
		return
	}

	sess, err := capture.Start(g.cam, capture.Options{
		BaseDir:     base,
		IdleTimeout: idle,
		DeviceName:  g.cam.Info.Name,
		Format:      fmtStr,
	})
	if err != nil {
		walk.MsgBox(g.mw, "오류", err.Error(), walk.MsgBoxIconError)
		g.startPreview()
		return
	}

	g.saveCfg()
	g.busy.Store(true)
	g.sess = sess
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel

	g.startBtn.SetEnabled(false)
	g.stopBtn.SetEnabled(true)
	g.manualBtn.SetEnabled(true)
	g.openBtn.SetEnabled(false)
	g.camCB.SetEnabled(false)
	g.fmtCB.SetEnabled(false)
	_ = g.countLbl.SetText("저장 0장 · 0s")
	g.setStatus("추출 중… (미리보기 일시 중지 · [추출 정지] 또는 자동 종료)")

	go func() {
		// Do not run preview Grab alongside capture.Session (single camera consumer).
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					c, elapsed, rem, ok := sess.Stats()
					g.mw.Synchronize(func() {
						_ = g.countLbl.SetText(fmt.Sprintf("저장 %d장 · %s", c, elapsed.Round(time.Second)))
						if ok {
							_ = g.idleLbl.SetText(fmt.Sprintf("자동 종료까지 %s", rem.Round(time.Second)))
						} else {
							_ = g.idleLbl.SetText("")
						}
					})
				}
			}
		}()

		res := sess.Run(ctx)
		g.mw.Synchronize(func() {
			g.finishExtract(res)
		})
	}()
}

func (g *gui) finishExtract(res capture.Result) {
	g.stopPreview()
	g.busy.Store(false)
	g.cancel = nil
	g.startBtn.SetEnabled(true)
	g.stopBtn.SetEnabled(false)
	g.manualBtn.SetEnabled(false)
	g.openBtn.SetEnabled(g.sess != nil && g.sess.Dir() != "")
	g.camCB.SetEnabled(true)
	g.fmtCB.SetEnabled(true)

	msg := fmt.Sprintf("종료: %s · %d장", res.Reason, res.Count)
	if res.Err != nil {
		msg += " · " + res.Err.Error()
	}
	g.setStatus(msg)
	_ = g.countLbl.SetText(fmt.Sprintf("저장 %d장", res.Count))
	_ = g.idleLbl.SetText("")
	g.startPreview()
}

func (g *gui) stopExtract() {
	if g.cancel != nil {
		g.cancel()
	}
	if g.sess != nil {
		g.sess.Stop()
	}
}

func (g *gui) manualCapture() {
	if g.sess == nil {
		return
	}
	if err := g.sess.ManualCapture(); err != nil {
		g.setStatus(err.Error())
		return
	}
	g.setStatus("수동 캡처 저장됨")
}

func (g *gui) openSession() {
	if g.sess == nil || g.sess.Dir() == "" {
		return
	}
	_ = exec.Command("explorer.exe", g.sess.Dir()).Start()
}
