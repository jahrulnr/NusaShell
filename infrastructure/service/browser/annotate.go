package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// markWait bounds one mark: hit test, descriptor, paint, and capture.
const markWait = 20 * time.Second

// descriptorExtractor runs with this bound to the picked element. Per level
// the selector is tag#id, else tag.cls.cls (two sane classes at most), else
// tag:nth-of-type; four ancestors at most, shadow boundaries joined with
// " >>> ". The rect is in top-viewport CSS pixels, frame offsets included.
const descriptorExtractor = `function(){
const e=this;
if(!e||e.nodeType!==1)return{missed:true};
const cssv=s=>String(s).replace(/([^\w-])/g,'\\$1');
const seg=el=>{
	const tag=el.tagName.toLowerCase();
	if(el.id)return tag+'#'+cssv(el.id);
	const cls=[...el.classList].filter(c=>/^[A-Za-z_][\w-]*$/.test(c)).slice(0,2);
	if(cls.length)return tag+'.'+cls.map(cssv).join('.');
	const p=el.parentElement;
	if(p){const same=[...p.children].filter(c=>c.tagName===el.tagName);
		if(same.length>1)return tag+':nth-of-type('+(same.indexOf(el)+1)+')';}
	return tag;
};
const nth=el=>{
	const tag=el.tagName.toLowerCase();const p=el.parentElement;
	if(!p)return tag;
	const same=[...p.children].filter(c=>c.tagName===el.tagName);
	return same.length>1?tag+':nth-of-type('+(same.indexOf(el)+1)+')':tag;
};
let chains=[[]],node=e,d=0;
while(node&&node.nodeType===1&&d<4){
	chains[0].unshift(seg(node));d++;
	const root=node.getRootNode();
	if(root&&root.host){node=root.host;chains.unshift([]);continue;}
	node=node.parentElement;
}
const selector=chains.map(c=>c.join(' > ')).join(' >>> ');
let pchains=[[]],pn=e,pd=0;
while(pn&&pn.nodeType===1&&pd<24){
	pchains[0].unshift(nth(pn));pd++;
	const root=pn.getRootNode();
	if(root&&root.host){pn=root.host;pchains.unshift([]);continue;}
	pn=pn.parentElement;
}
const elementPath=pchains.map(c=>c.join(' > ')).join(' >>> ');
const framePath=[];let offX=0,offY=0;
// Accumulate frameElement rects so rect is top-viewport CSS px like the
// client's click point — the pin marker is painted in the main document.
try{let w=window;while(w.frameElement){const fr=w.frameElement.getBoundingClientRect();
	offX+=fr.left;offY+=fr.top;framePath.unshift(seg(w.frameElement));w=w.parent;}}catch(_){}
const roleMap={BUTTON:'button',SELECT:'combobox',TEXTAREA:'textbox',NAV:'navigation',MAIN:'main',
	ASIDE:'complementary',H1:'heading',H2:'heading',H3:'heading',H4:'heading',H5:'heading',
	H6:'heading',UL:'list',OL:'list',LI:'listitem',TABLE:'table',IMG:'img',DIALOG:'dialog',FORM:'form'};
let role=e.getAttribute('role')||'';
if(!role){
	if(e.tagName==='INPUT'){const t=(e.getAttribute('type')||'text').toLowerCase();
		role={checkbox:'checkbox',radio:'radio',range:'slider',number:'spinbutton',
			search:'searchbox',submit:'button',button:'button',reset:'button',image:'button'}[t]||'textbox';}
	else if(e.tagName==='A'){role=e.hasAttribute('href')?'link':'';}
	else role=roleMap[e.tagName]||'';
}
let name='';
const aria=e.getAttribute('aria-label');if(aria)name=aria.trim();
if(!name){const lb=e.getAttribute('aria-labelledby');
	if(lb){name=lb.split(/\s+/).map(id=>{const n=e.ownerDocument.getElementById(id);return n?n.textContent:''}).join(' ').trim();}}
if(!name){const al=e.getAttribute('alt')||e.getAttribute('title')||e.getAttribute('placeholder');if(al)name=al.trim();}
if(!name&&(e.tagName==='A'||e.tagName==='BUTTON'||role==='button'||role==='link'))name=(e.textContent||'').trim();
if(!name){try{const lab=e.labels&&e.labels[0];if(lab)name=(lab.textContent||'').trim();}catch(_){}}
let immediate=[...e.childNodes].filter(n=>n.nodeType===3).map(n=>n.textContent).join('').trim();
if(!immediate)immediate=(e.innerText||'').trim();
immediate=immediate.replace(/\s+/g,' ').slice(0,200);
let nearby='';
try{const p=e.parentElement;
	if(p){let t=(p.innerText||'').trim();
		const et=(e.innerText||'').trim();
		if(et&&t.includes(et))t=t.replace(et,'');
		nearby=t.replace(/\s+/g,' ').trim().slice(0,300);}}catch(_){}
let fixed=false;
for(let n=e;n&&n.nodeType===1;n=n.parentElement){const pos=getComputedStyle(n).position;
	if(pos==='fixed'||pos==='sticky'){fixed=true;break;}}
const r=e.getBoundingClientRect();
const de=document.documentElement;
const cs=getComputedStyle(e);
let html='';
try{const oh=e.outerHTML||'';const gi=oh.indexOf('>');
	html=(gi>=0?oh.slice(0,gi+1):oh).slice(0,400);}catch(_){}
return{missed:false,
	selector:selector,elementPath:elementPath,tagName:e.tagName.toLowerCase(),
	html:html,
	role:role,name:name.slice(0,200),immediateText:immediate,nearbyText:nearby,
	rect:{x:r.x+offX,y:r.y+offY,width:r.width,height:r.height},isFixed:fixed,
	framePath:framePath,pageUrl:location.href,title:document.title,
	viewport:{width:window.innerWidth||0,height:window.innerHeight||0,dpr:window.devicePixelRatio||1},
	style:{fontSize:cs.fontSize,lineHeight:cs.lineHeight,fontFamily:cs.fontFamily,
		fontWeight:cs.fontWeight,letterSpacing:cs.letterSpacing,color:cs.color,
		backgroundColor:cs.backgroundColor,display:cs.display,position:cs.position},
	scrollX:window.scrollX||0,scrollY:window.scrollY||0,
	pageW:Math.max(de?de.scrollWidth:0,document.body?document.body.scrollWidth:0),
	pageH:Math.max(de?de.scrollHeight:0,document.body?document.body.scrollHeight:0)};
}`

// pickElement is the topmost element at a viewport point, in the same
// coordinates the live view shows, so a scrolled page still hits right.
// Same-origin frames are entered; a cross-origin frame is the <iframe>.
const pickElement = `function(x,y){
var doc=document,el=null;
for(var d=0;d<8;d++){
	el=doc.elementFromPoint(x,y);
	if(!el)return null;
	var t=el.tagName;
	if(t!=='IFRAME'&&t!=='FRAME')return el;
	var inner=null;
	try{inner=el.contentDocument;}catch(e){}
	if(!inner)return el;
	var r=el.getBoundingClientRect();
	x-=r.left;y-=r.top;doc=inner;
}
return el;}`

// markerPainter paints the element's outline and the numbered badge as
// plain DOM, so the screenshot shows what the agent is told: the badge
// number matches the [N] line of the annotation note.
const markerPainter = `function(r,n){
var old=document.getElementById('__nusashell_marker');if(old)old.remove();
var host=document.createElement('div');
host.id='__nusashell_marker';
host.setAttribute('style','position:fixed !important;left:0;top:0;width:0;height:0;z-index:2147483647;pointer-events:none;');
var ring=document.createElement('div');
ring.setAttribute('style','position:fixed !important;left:'+r.x+'px;top:'+r.y+'px;width:'+Math.max(2,r.width)+'px;height:'+Math.max(2,r.height)+'px;border:2px solid #3b82f6 !important;background:rgba(59,130,246,.14) !important;border-radius:3px;box-sizing:border-box;');
var vw=window.innerWidth||1280,vh=window.innerHeight||800;
var bx=r.x+r.width/2,by=r.y;
bx=Math.max(14,Math.min(bx,vw-14));
if(by<14)by=r.y+r.height;
by=Math.max(14,Math.min(by,vh-14));
var badge=document.createElement('div');
badge.setAttribute('style','position:fixed !important;left:'+bx+'px;top:'+by+'px;transform:translate(-50%,-50%);min-width:20px;height:20px;padding:0 5px;border-radius:10px;background:#3b82f6 !important;color:#fff !important;font:600 12px/20px system-ui,-apple-system,"Segoe UI",sans-serif;text-align:center;box-shadow:0 0 0 2px #fff,0 1px 4px rgba(0,0,0,.35);box-sizing:content-box;');
badge.textContent=String(n);
host.appendChild(ring);host.appendChild(badge);
(document.documentElement||document.body).appendChild(host);
// Wait two frames so the pin is painted before the caller screenshots —
// with a timeout fallback: a throttled (backgrounded) tab may starve rAF.
return new Promise(function(res){var t=setTimeout(res,250);requestAnimationFrame(function(){requestAnimationFrame(function(){clearTimeout(t);res()})})});}`

const markerRemover = `(function(){var m=document.getElementById('__nusashell_marker');if(m)m.remove();return true})()`

// regionMarkerPainter strokes a drawn region dashed, so it reads as a drawn
// area rather than an element's box; a null rect (a page mark) paints only
// the badge at the top of the viewport.
const regionMarkerPainter = `function(r,n){
var old=document.getElementById('__nusashell_marker');if(old)old.remove();
var host=document.createElement('div');
host.id='__nusashell_marker';
host.setAttribute('style','position:fixed !important;left:0;top:0;width:0;height:0;z-index:2147483647;pointer-events:none;');
var vw=window.innerWidth||1280,vh=window.innerHeight||800;
var bx,by;
if(r){
	var ring=document.createElement('div');
	ring.setAttribute('style','position:fixed !important;left:'+r.x+'px;top:'+r.y+'px;width:'+Math.max(2,r.width)+'px;height:'+Math.max(2,r.height)+'px;border:2px dashed #3b82f6 !important;background:rgba(59,130,246,.10) !important;border-radius:3px;box-sizing:border-box;');
	host.appendChild(ring);
	bx=r.x+r.width/2;by=r.y;
	bx=Math.max(14,Math.min(bx,vw-14));
	if(by<14)by=r.y+r.height;
	by=Math.max(14,Math.min(by,vh-14));
}else{
	bx=vw/2;by=16;
}
var badge=document.createElement('div');
badge.setAttribute('style','position:fixed !important;left:'+bx+'px;top:'+by+'px;transform:translate(-50%,-50%);min-width:20px;height:20px;padding:0 5px;border-radius:10px;background:#3b82f6 !important;color:#fff !important;font:600 12px/20px system-ui,-apple-system,"Segoe UI",sans-serif;text-align:center;box-shadow:0 0 0 2px #fff,0 1px 4px rgba(0,0,0,.35);box-sizing:content-box;');
badge.textContent=String(n);
host.appendChild(badge);
(document.documentElement||document.body).appendChild(host);
return new Promise(function(res){var t=setTimeout(res,250);requestAnimationFrame(function(){requestAnimationFrame(function(){clearTimeout(t);res()})})});}`

// pageInfoExtractor is the page half of the descriptor, for region and page
// marks.
const pageInfoExtractor = `(function(){var de=document.documentElement;return{
pageUrl:location.href,title:document.title,
viewport:{width:window.innerWidth||0,height:window.innerHeight||0,dpr:window.devicePixelRatio||1},
scrollX:window.scrollX||0,scrollY:window.scrollY||0,
pageW:Math.max(de?de.scrollWidth:0,document.body?document.body.scrollWidth:0),
pageH:Math.max(de?de.scrollHeight:0,document.body?document.body.scrollHeight:0)}})()`

// pickObject hit-tests a viewport point; "" is a point with no element.
// The object may live in a frame's context; calls on its id run there.
func pickObject(c context.Context, x, y float64) (cdpruntime.RemoteObjectID, error) {
	res, err := chromedp.Call(c, cdpruntime.Evaluate, cdpruntime.EvaluateParams{
		Expression: fmt.Sprintf("(%s)(%g,%g)", pickElement, x, y)})
	if err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", errors.New(res.ExceptionDetails.Text)
	}
	if res.Result == nil {
		return "", nil
	}
	return res.Result.ObjectID, nil
}

// describe runs the descriptor extractor on a picked element; nil raw is
// an element the extractor refused.
func describe(c context.Context, obj cdpruntime.RemoteObjectID) (json.RawMessage, error) {
	res, err := chromedp.Call(c, cdpruntime.CallFunctionOn, cdpruntime.CallFunctionOnParams{
		FunctionDeclaration: descriptorExtractor, ObjectID: obj, ReturnByValue: new(true)})
	if err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		return nil, errors.New(res.ExceptionDetails.Text)
	}
	if res.Result == nil {
		return nil, nil
	}
	var probe struct{ Missed bool }
	if json.Unmarshal(res.Result.Value, &probe) != nil || probe.Missed {
		return nil, nil
	}
	return res.Result.Value, nil
}

// PreviewAnnotate never queues behind a mark in flight.
func (s *Session) PreviewAnnotate(ctx context.Context, x, y float64) (dservice.BrowserMarkPreview, error) {
	if s.annotating.Load() {
		return dservice.BrowserMarkPreview{Skipped: true}, nil
	}
	c, cancel := s.call(ctx, 5*time.Second)
	defer cancel()
	obj, err := pickObject(c, x, y)
	if err != nil {
		return dservice.BrowserMarkPreview{}, s.fail(err)
	}
	if obj == "" {
		return dservice.BrowserMarkPreview{Missed: true}, nil
	}
	raw, err := describe(c, obj)
	if err != nil {
		return dservice.BrowserMarkPreview{}, s.fail(err)
	}
	if raw == nil {
		return dservice.BrowserMarkPreview{Missed: true}, nil
	}
	var d valueobject.AnnotationDescriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		return dservice.BrowserMarkPreview{}, fmt.Errorf("annotate preview: %w", err)
	}
	return dservice.BrowserMarkPreview{Rect: &d.Rect, TagName: d.TagName, Name: d.Label(), Selector: d.Selector}, nil
}

// Annotate is hit test (elements) or page info (regions, pages), then the
// badge, a full-viewport capture — a clipped capture resizes the visual
// viewport for a moment and leaks a wrongly sized frame into the live
// view — and the badge removed again.
func (s *Session) Annotate(ctx context.Context, m dservice.BrowserMark) (valueobject.AnnotationDescriptor, []byte, error) {
	var d valueobject.AnnotationDescriptor
	if m.Kind == enum.AnnotationRegion && (m.Width <= 0 || m.Height <= 0) {
		return d, nil, apperror.New(400, "a region needs a positive width and height", nil)
	}
	if !s.annotating.CompareAndSwap(false, true) {
		return d, nil, apperror.New(409, "another mark is still being made", nil)
	}
	defer s.annotating.Store(false)
	c, cancel := s.call(ctx, markWait)
	defer cancel()
	marker := max(m.Marker, 1)
	var paint string
	switch m.Kind {
	case enum.AnnotationElement:
		obj, err := pickObject(c, m.X, m.Y)
		if err != nil {
			return d, nil, s.fail(err)
		}
		var raw json.RawMessage
		if obj != "" {
			if raw, err = describe(c, obj); err != nil {
				return d, nil, s.fail(err)
			}
		}
		if raw == nil {
			return d, nil, apperror.New(422, "no element at that point", nil)
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			return d, nil, fmt.Errorf("annotate: %w", err)
		}
		d.Point = valueobject.AnnotationPoint{X: m.X, Y: m.Y}
		rect, _ := json.Marshal(d.Rect)
		paint = fmt.Sprintf("(%s)(%s,%d)", markerPainter, rect, marker)
	case enum.AnnotationRegion, enum.AnnotationPage:
		res, err := chromedp.Call(c, cdpruntime.Evaluate, cdpruntime.EvaluateParams{Expression: pageInfoExtractor, ReturnByValue: new(true)})
		if err != nil {
			return d, nil, s.fail(err)
		}
		if res.ExceptionDetails != nil || res.Result == nil {
			return d, nil, errors.New("annotate: the page info could not be read")
		}
		if err := json.Unmarshal(res.Result.Value, &d); err != nil {
			return d, nil, fmt.Errorf("annotate: %w", err)
		}
		rect := []byte("null")
		if m.Kind == enum.AnnotationRegion {
			// A region may sit a little outside the viewport; only absurd
			// magnitudes are clamped.
			const most = 16384
			d.Rect = valueobject.AnnotationRect{X: min(max(m.X, -most), most), Y: min(max(m.Y, -most), most),
				Width: min(m.Width, most), Height: min(m.Height, most)}
			rect, _ = json.Marshal(d.Rect)
		}
		paint = fmt.Sprintf("(%s)(%s,%d)", regionMarkerPainter, rect, marker)
	default:
		return d, nil, apperror.New(400, "unknown annotation kind "+m.Kind.String(), nil)
	}
	d.Kind = m.Kind
	if _, err := chromedp.Call(c, cdpruntime.Evaluate, cdpruntime.EvaluateParams{Expression: paint, AwaitPromise: new(true)}); err != nil {
		s.log.Warn("annotation badge not painted; the screenshot is unmarked", "error", err)
	}
	shot, err := chromedp.Call(c, page.CaptureScreenshot, page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatPng})
	// Its own budget: the capture may have spent c's.
	s.removeBadge(context.WithoutCancel(ctx))
	if err != nil {
		return d, nil, s.fail(err)
	}
	return d, shot.Data, nil
}

// ClearAnnotate is best-effort. A mark in flight owns its badge, so a
// clear landing mid-mark is a no-op rather than stripping the badge
// between paint and capture.
func (s *Session) ClearAnnotate(ctx context.Context) {
	if !s.annotating.CompareAndSwap(false, true) {
		return
	}
	defer s.annotating.Store(false)
	s.removeBadge(ctx)
}

func (s *Session) removeBadge(ctx context.Context) {
	c, cancel := s.call(ctx, 5*time.Second)
	defer cancel()
	_, _ = chromedp.Call(c, cdpruntime.Evaluate, cdpruntime.EvaluateParams{Expression: markerRemover})
}
