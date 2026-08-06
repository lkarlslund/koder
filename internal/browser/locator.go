package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/browserapi"
)

func validateLocator(ctx context.Context, locator browserapi.Locator, action string) error {
	var found bool
	return chromedp.Run(ctx, chromedp.Evaluate(`Boolean(`+locatorExpression(locator, action)+`)`, &found))
}

func locatorExpression(locator browserapi.Locator, action string) string {
	if locator.Empty() && action == "press" {
		return `(document.activeElement||document.body)`
	}
	config, _ := json.Marshal(locator)
	return fmt.Sprintf(`(()=>{const cfg=%s;%s;return resolve(cfg,%q)})()`, config, semanticResolverJS, action)
}

func dragExpression(source, target browserapi.Locator) string {
	sourceConfig, _ := json.Marshal(source)
	targetConfig, _ := json.Marshal(target)
	return fmt.Sprintf(`(()=>{%s;const a=resolve(%s,'drag');const b=resolve(%s,'drag');const d=new DataTransfer();a.dispatchEvent(new DragEvent('dragstart',{bubbles:true,dataTransfer:d}));for(const type of ['dragenter','dragover','drop'])b.dispatchEvent(new DragEvent(type,{bubbles:true,cancelable:true,dataTransfer:d}));a.dispatchEvent(new DragEvent('dragend',{bubbles:true,dataTransfer:d}));return true})()`, semanticResolverJS, sourceConfig, targetConfig)
}

func scrollExpression(locator browserapi.Locator, x, y int) string {
	if locator.Empty() {
		return fmt.Sprintf(`(()=>{window.scrollBy(%d,%d);return true})()`, x, y)
	}
	return fmt.Sprintf(`(()=>{%s;resolve(%s,'scroll').scrollBy(%d,%d);return true})()`, semanticResolverJS, mustJSON(locator), x, y)
}

func boundsExpression(locator browserapi.Locator) string {
	return fmt.Sprintf(`(()=>{%s;const e=resolve(%s,'capture');e.scrollIntoView({block:'nearest',inline:'nearest'});const r=e.getBoundingClientRect();return {x:r.left+window.scrollX,y:r.top+window.scrollY,width:r.width,height:r.height,scale:1}})()`, semanticResolverJS, mustJSON(locator))
}

func interactionExpression(locator browserapi.Locator, action, value string) string {
	return fmt.Sprintf(`(()=>{%s;const e=resolve(%s,%q);const value=%s;switch(%q){case 'click':e.click();break;case 'fill':{e.focus();const proto=e instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:e instanceof HTMLSelectElement?HTMLSelectElement.prototype:HTMLInputElement.prototype;const setter=Object.getOwnPropertyDescriptor(proto,'value')?.set;if(setter)setter.call(e,value);else e.value=value;e.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:value}));e.dispatchEvent(new Event('change',{bubbles:true}));break;}case 'type':case 'press':e.focus();break;case 'select':e.value=value;e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));break;case 'check':if(!e.checked)e.click();break;case 'uncheck':if(e.checked)e.click();break;case 'hover':e.dispatchEvent(new MouseEvent('mouseover',{bubbles:true}));e.dispatchEvent(new MouseEvent('mouseenter',{bubbles:true}));break;}return true})()`, semanticResolverJS, mustJSON(locator), action, mustJSON(value), action)
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

const semanticResolverJS = `
const resolve=(cfg,action)=>{
  const norm=value=>(value||'').replace(/\u00a0/g,' ').replace(/[\u2018\u2019]/g,"'").replace(/[\u2013\u2014]/g,'-').replace(/\s+/g,' ').trim();
  const lower=value=>norm(value).toLocaleLowerCase();
  const normalizedRole=value=>{const result=lower(value);return result==='img'?'image':result};
  const visible=el=>{
    const style=getComputedStyle(el);
    return style.display!=='none'&&style.visibility!=='hidden'&&style.opacity!=='0'&&el.getClientRects().length>0;
  };
  const implicitRole=el=>{
    const tag=el.tagName;
    if(tag==='A'&&el.hasAttribute('href'))return 'link';
    if(tag==='BUTTON')return 'button';
    if(tag==='TEXTAREA')return 'textbox';
    if(tag==='SELECT')return el.multiple?'listbox':'combobox';
    if(tag==='IMG')return 'image';
	if(tag==='CANVAS'||tag==='SVG')return 'image';
    if(tag==='INPUT'){
      const type=(el.type||'text').toLowerCase();
      if(type==='checkbox')return 'checkbox';
      if(type==='radio')return 'radio';
      if(['button','submit','reset','image'].includes(type))return 'button';
      if(type==='range')return 'slider';
      return 'textbox';
    }
    if(/^H[1-6]$/.test(tag))return 'heading';
    return '';
  };
  const role=el=>normalizedRole(el.getAttribute('role')||implicitRole(el));
  const labelledBy=el=>norm((el.getAttribute('aria-labelledby')||'').split(/\s+/).filter(Boolean).map(id=>document.getElementById(id)?.innerText||document.getElementById(id)?.textContent||'').join(' '));
	const labelText=item=>{const clone=item.cloneNode(true);for(const control of clone.querySelectorAll('input,select,textarea,button'))control.remove();return clone.textContent||''};
	const label=el=>norm(el.labels&&el.labels.length?[...el.labels].map(labelText).join(' '):'');
  const sourceHint=el=>el.tagName==='IMG'?norm((el.currentSrc||el.src||'').split(/[?#]/,1)[0].replace(/[^a-zA-Z0-9]+/g,' ')):'';
  const name=el=>norm(labelledBy(el)||el.getAttribute('aria-label')||label(el)||el.alt||el.title||el.placeholder||el.innerText||el.value||el.textContent||sourceHint(el)||'');
  const searchableName=el=>lower(name(el)+' '+sourceHint(el));
  const matches=(candidate,wanted,exact)=>exact?lower(name(candidate))===wanted:searchableName(candidate).includes(wanted)||wanted.split(' ').filter(Boolean).every(token=>searchableName(candidate).includes(token));
  const collect=root=>{
    const out=[];
    for(const el of root.querySelectorAll('*')){
      out.push(el);
      if(el.shadowRoot)out.push(...collect(el.shadowRoot));
    }
    return out;
  };
  const allowed=el=>{
    const tag=el.tagName;
    const type=(el.type||'').toLowerCase();
    const r=role(el);
    switch(action){
		case 'click':return ['button','link','checkbox','radio','menuitem','option','tab','switch'].includes(r)||el.onclick||el.tabIndex>=0||(tag==='IMG'&&Boolean(el.closest('a,button,[role="button"],[role="link"]')));
		case 'fill':case 'type':return tag==='TEXTAREA'||el.isContentEditable||(tag==='INPUT'&&!['button','submit','reset','checkbox','radio','file','hidden'].includes(type));
		case 'press':return ['INPUT','TEXTAREA','SELECT','BUTTON','A'].includes(tag)||el.isContentEditable||el.tabIndex>=0;
      case 'select':return tag==='SELECT'||r==='listbox'||r==='combobox';
      case 'check':case 'uncheck':return type==='checkbox'||type==='radio'||r==='checkbox'||r==='radio'||r==='switch';
      case 'upload':return tag==='INPUT'&&type==='file';
      default:return true;
    }
  };
  const inScope=el=>{
    if(!cfg.within)return true;
    const wanted=lower(cfg.within);
    for(let parent=el.parentElement;parent;parent=parent.parentElement){
      const candidate=lower(name(parent)||parent.innerText||parent.textContent);
      if(candidate.includes(wanted))return true;
    }
    return false;
  };
  let candidates=[];
  if(cfg.selector){
    if(cfg.selector.startsWith('xpath=')){
      const result=document.evaluate(cfg.selector.slice(6),document,null,XPathResult.ORDERED_NODE_SNAPSHOT_TYPE,null);
      for(let i=0;i<result.snapshotLength;i++){const node=result.snapshotItem(i);if(node&&node.nodeType===Node.ELEMENT_NODE)candidates.push(node)}
    }else{
      try{candidates=[...document.querySelectorAll(cfg.selector)]}catch(error){throw new Error('Invalid browser selector: '+error.message)}
    }
	  }else{
	    const wanted=lower(cfg.target);
    const wantedRole=normalizedRole(cfg.role);
    candidates=collect(document).filter(el=>{
      if(!allowed(el)||!inScope(el))return false;
      if(action!=='capture'&&action!=='upload'&&!visible(el))return false;
      if(wantedRole&&role(el)!==wantedRole)return false;
	      return matches(el,wanted,Boolean(cfg.exact));
	    });
	    if(!cfg.exact&&candidates.length>1){
	      const exactMatches=candidates.filter(el=>lower(name(el))===wanted);
	      if(exactMatches.length)candidates=exactMatches;
	      else{
	        const semanticMatches=candidates.filter(el=>role(el)!=='');
	        if(semanticMatches.length)candidates=semanticMatches;
	      }
	    }
	  }
  const selectorNeedsActionType=['fill','type','select','check','uncheck','upload'].includes(action);
  if(!cfg.selector||selectorNeedsActionType)candidates=candidates.filter(allowed);
  candidates=candidates.filter(inScope);
  if(action!=='upload')candidates=candidates.filter(visible);
  const occurrence=Number(cfg.occurrence||0);
  if(occurrence>0){
    if(occurrence>candidates.length)throw new Error('Browser target occurrence '+occurrence+' not found; matched '+candidates.length+' element(s)');
    return candidates[occurrence-1];
  }
  if(candidates.length>1&&(action==='capture'||(action==='click'&&!cfg.selector))&&candidates.every(el=>role(el)==='image')){
    const ranked=[...candidates].sort((a,b)=>{const ar=a.getBoundingClientRect(),br=b.getBoundingClientRect();return br.width*br.height-ar.width*ar.height});
    const first=ranked[0].getBoundingClientRect(),second=ranked[1].getBoundingClientRect();
    if(first.width*first.height>second.width*second.height)candidates=[ranked[0]];
  }
  if(candidates.length===1)return candidates[0];
  const describe=el=>{
    const r=role(el)||el.tagName.toLowerCase();
    const n=name(el);
		const rect=el.getBoundingClientRect();
		const size=rect.width>0&&rect.height>0?' '+Math.round(rect.width)+'x'+Math.round(rect.height):'';
    return r+(n?' "'+n.slice(0,120)+'"':'')+size;
  };
  const requested=cfg.selector?'selector "'+cfg.selector+'"':'target "'+cfg.target+'"'+(cfg.role?' with role "'+cfg.role+'"':'');
  if(candidates.length===0)throw new Error('Browser '+requested+' was not found in the current DOM');
  throw new Error('Browser '+requested+' is ambiguous; matched '+candidates.length+' elements: '+candidates.slice(0,5).map((el,index)=>(index+1)+'. '+describe(el)).join('; ')+'. Refine with role, within, exact, occurrence, or selector.');
};`
