async page => {
 await page.reload(); await page.waitForFunction(()=>!!window.PR?.blockById);
 await page.evaluate(()=>PR.openPaperNote());
 const before=await page.locator('#paperNote').inputValue();
 try {
 const capture=await page.evaluate(()=>{
  const section=document.createElement('section');section.className='blk';section.dataset.id='test-reference';
  PR.blockById['test-reference']={id:'test-reference',type:'table',page:3};
  section.innerHTML='<div class="tbl-wrap"><table><tr><th><strong>名称</strong></th><th>数值</th></tr><tr><td>A</td><td><em>123</em></td></tr></table></div>';
  document.querySelector('#paper').append(section);
  const cell=section.querySelector('td'),r=document.createRange();r.selectNodeContents(cell);
  const html=PR.capturePaperQuote(section.querySelector('.tbl-wrap'),r);
  return {html};
 });
 await page.evaluate(async html=>{await PR.paperNoteEditor.insertSources([{anchor:'test-reference',quote:'A',quoteHTML:html}]);},capture.html);
 if(await page.locator('.pn-prose table tr').count()<2)throw Error('whole table lost');
 if(!await page.locator('.pn-prose table strong').count())throw Error('bold lost');
 if(!await page.locator('.pn-prose table em').count())throw Error('italic lost');
 const markdown=await page.locator('#paperNote').inputValue();
 if(!markdown.includes('123')||!markdown.includes('名称'))throw Error('table not saved');
 return {wholeTable:true,bold:true,italic:true,markdown:true};
 }finally{
 await page.locator('[data-note-mode=source]').click();await page.locator('#paperNote').fill(before);await page.evaluate(()=>PR.finishPanelNote());
 await page.evaluate(()=>{document.querySelector('#b-test-reference')?.remove();delete PR.blockById['test-reference'];});
 }
}
