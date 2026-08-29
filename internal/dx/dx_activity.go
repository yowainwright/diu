package dx

import (
	"sync"
	"time"
)

const (
	activityInterval = 90 * time.Millisecond
	clearLine        = "\r\x1b[2K"
	hideCursor       = "\x1b[?25l"
	showCursor       = "\x1b[?25h"
)

var activityFrames = [...]string{"-", "\\", "|", "/"}

type Activity struct {
	out        *Out
	message    string
	frame      int
	enabled    bool
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	finishOnce sync.Once
	mu         sync.Mutex
}

func (o *Out) StartActivity(message string) *Activity {
	activity := &Activity{
		out:     o,
		message: message,
		enabled: o.CanAnimate(),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	activity.start()
	return activity
}

func (a *Activity) Update(message string) {
	a.mu.Lock()
	a.message = message
	a.mu.Unlock()
}

func (a *Activity) Stop() {
	a.finishOnce.Do(a.stopAnimation)
}

func (a *Activity) Success(message string) {
	a.finish(Success, message)
}

func (a *Activity) Warning(message string) {
	a.finish(Warning, message)
}

func (a *Activity) Fail(message string) {
	a.finish(Error, message)
}

func (a *Activity) Notice(tone Tone, message string) {
	a.stopAnimation()
	a.out.Status(tone, message)
}

func (a *Activity) start() {
	if !a.enabled {
		close(a.done)
		return
	}
	a.out.UI(hideCursor)
	a.render()
	go a.spin()
}

func (a *Activity) spin() {
	defer close(a.done)
	ticker := time.NewTicker(activityInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.advance()
		case <-a.stop:
			return
		}
	}
}

func (a *Activity) advance() {
	a.mu.Lock()
	a.frame = (a.frame + 1) % len(activityFrames)
	a.mu.Unlock()
	a.render()
}

func (a *Activity) render() {
	a.mu.Lock()
	frame := activityFrames[a.frame]
	message := a.message
	a.mu.Unlock()
	a.out.UI(clearLine + a.out.UIStyle(Accent, frame) + " " + message)
}

func (a *Activity) finish(tone Tone, message string) {
	a.finishOnce.Do(func() {
		a.stopAnimation()
		a.out.Status(tone, message)
	})
}

func (a *Activity) stopAnimation() {
	a.stopOnce.Do(a.stopLoop)
}

func (a *Activity) stopLoop() {
	if !a.enabled {
		return
	}
	close(a.stop)
	<-a.done
	a.out.UI(clearLine + showCursor)
}

func (o *Out) Shine(text string) string {
	return o.UIStyle(Accent, text)
}

func (o *Out) Glitter(message string) {
	o.Status(Accent, message)
}
