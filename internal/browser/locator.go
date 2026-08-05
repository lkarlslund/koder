package browser

import (
	"encoding/json"
	"fmt"

	"github.com/lkarlslund/koder/internal/browserapi"
)

func locatorExpression(locator browserapi.Locator, action string) string {
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

func interactionExpression(locator browserapi.Locator, action, value string) string {
	return fmt.Sprintf(`(()=>{%s;const e=resolve(%s,%q);const value=%s;switch(%q){case 'select':e.value=value;e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));break;case 'check':if(!e.checked)e.click();break;case 'uncheck':if(e.checked)e.click();break;case 'hover':e.dispatchEvent(new MouseEvent('mouseover',{bubbles:true}));e.dispatchEvent(new MouseEvent('mouseenter',{bubbles:true}));break;}return true})()`, semanticResolverJS, mustJSON(locator), action, mustJSON(value), action)
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

const semanticResolverJS = `
const resolve=(cfg,action)=>{
  const norm=value=>(value||'').replace(/\s+/g,' ').trim();
  const lower=value=>norm(value).toLocaleLowerCase();
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
  const role=el=>lower(el.getAttribute('role')||implicitRole(el));
  const labelledBy=el=>norm((el.getAttribute('aria-labelledby')||'').split(/\s+/).filter(Boolean).map(id=>document.getElementById(id)?.innerText||document.getElementById(id)?.textContent||'').join(' '));
  const label=el=>norm(el.labels&&el.labels.length?[...el.labels].map(item=>item.innerText||item.textContent||'').join(' '):'');
  const name=el=>norm(labelledBy(el)||el.getAttribute('aria-label')||label(el)||el.alt||el.title||el.placeholder||el.innerText||el.value||el.textContent||'');
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
      case 'click':return ['button','link','checkbox','radio','menuitem','option','tab','switch'].includes(r)||el.onclick||el.tabIndex>=0;
      case 'fill':case 'type':return tag==='TEXTAREA'||el.isContentEditable||(tag==='INPUT'&&!['button','submit','reset','checkbox','radio','file','hidden'].includes(type));
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
    const wantedRole=lower(cfg.role);
    candidates=collect(document).filter(el=>{
      if(!allowed(el)||!inScope(el))return false;
      if(action!=='capture'&&action!=='upload'&&!visible(el))return false;
      if(wantedRole&&role(el)!==wantedRole)return false;
      const candidate=lower(name(el));
      return cfg.exact?candidate===wanted:candidate.includes(wanted);
    });
  }
  candidates=candidates.filter(allowed).filter(inScope);
  if(action!=='capture'&&action!=='upload')candidates=candidates.filter(visible);
  const occurrence=Number(cfg.occurrence||0);
  if(occurrence>0){
    if(occurrence>candidates.length)throw new Error('Browser target occurrence '+occurrence+' not found; matched '+candidates.length+' element(s)');
    return candidates[occurrence-1];
  }
  if(candidates.length===1)return candidates[0];
  const describe=el=>{
    const r=role(el)||el.tagName.toLowerCase();
    const n=name(el);
    return r+(n?' "'+n.slice(0,120)+'"':'');
  };
  const requested=cfg.selector?'selector "'+cfg.selector+'"':'target "'+cfg.target+'"'+(cfg.role?' with role "'+cfg.role+'"':'');
  if(candidates.length===0)throw new Error('Browser '+requested+' was not found in the current DOM');
  throw new Error('Browser '+requested+' is ambiguous; matched '+candidates.length+' elements: '+candidates.slice(0,5).map((el,index)=>(index+1)+'. '+describe(el)).join('; ')+'. Refine with role, within, exact, occurrence, or selector.');
};`
