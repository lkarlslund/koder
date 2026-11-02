package webui

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowserPreviewUsesCancelableIndependentRequests(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl := newTestController(t)
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = server.server.Close() })
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocator, stopAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	defer stopAllocator()
	browser, stopBrowser := chromedp.NewContext(allocator)
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(browser, 30*time.Second)
	defer cancel()
	var result struct{ FirstAborted, ClosedAborted, KeptImage, Independent, OwnerPinned bool }
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.connected === true && !!document.documentElement._x_dataStack[0].activeChatID() && !!document.documentElement._x_dataStack[0].currentSessionID()`, nil),
		chromedp.Evaluate(`(() => {
   const app=document.documentElement._x_dataStack[0];
   const originalFetch=window.fetch.bind(window);
   window.__previewRequests=[];
   window.fetch=(url,options)=>{
    if (!String(url).startsWith('/api/rpc/browser_tab_preview?')) return originalFetch(url,options);
    return new Promise((resolve,reject)=>{
     const request={url:String(url),signal:options.signal,body:JSON.parse(options.body)};
     request.complete=()=>resolve(new Response(JSON.stringify({ok:true,result:{mime:'image/png',image_base64:'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a5+8AAAAASUVORK5CYII='}}),{status:200,headers:{'Content-Type':'application/json'}}));
     request.fail=()=>reject(new Error('context deadline exceeded'));
     options.signal.addEventListener('abort',()=>reject(new DOMException('Canceled','AbortError')),{once:true});
     window.__previewRequests.push(request);
    });
   };
   const rpc=app.rpc.bind(app);
   app.rpc=(method,params)=>{if(method==='browser_tab_preview')throw new Error('preview must not use the WebSocket queue');return rpc(method,params)};
   app.browserPreview.rate=0;
   app.openBrowserPreview({id:'first',title:'First'});
   app.openBrowserPreview({id:'second',title:'Second'});
   window.__firstAborted=window.__previewRequests[0].signal.aborted;
   window.__previewRequests[1].complete();
  })()`, nil),
		chromedp.Poll(`document.documentElement._x_dataStack[0].browserPreview.loading === false && !!document.documentElement._x_dataStack[0].browserPreview.src`, nil),
		chromedp.Evaluate(`(() => {
   const app=document.documentElement._x_dataStack[0];
   app.refreshBrowserPreview(true);
   app.closeBrowserPreview();
   window.__closedAborted=window.__previewRequests[2].signal.aborted;
   app.openBrowserPreview({id:'third',title:'Third'});
   window.__previewRequests[3].complete();
  })()`, nil),
		chromedp.Poll(`document.documentElement._x_dataStack[0].browserPreview.loading === false && !!document.documentElement._x_dataStack[0].browserPreview.src`, nil),
		chromedp.Evaluate(`(() => {
   const app=document.documentElement._x_dataStack[0];
   window.__lastPreview=app.browserPreview.src;
   app.refreshBrowserPreview(true);
   window.__previewRequests[4].fail();
  })()`, nil),
		chromedp.Poll(`!!document.documentElement._x_dataStack[0].browserPreview.error && !document.documentElement._x_dataStack[0].browserPreview.loading`, nil),
		chromedp.Evaluate(`(() => {
   const app=document.documentElement._x_dataStack[0];
   const query=new URL(window.__previewRequests[0].url,location.origin).searchParams;
   return {
    FirstAborted:window.__firstAborted,ClosedAborted:window.__closedAborted,
    KeptImage:app.browserPreview.src===window.__lastPreview,
    Independent:window.__previewRequests.length===5,
    OwnerPinned:query.get('selected_chat')===app.activeChatID() && query.get('client_id')!==app.clientID
   };
  })()`, &result),
	)
	if err != nil {
		t.Fatalf("preview requests: %v", err)
	}
	if !result.FirstAborted || !result.ClosedAborted || !result.KeptImage || !result.Independent || !result.OwnerPinned {
		t.Fatalf("preview behavior=%#v", result)
	}
}
