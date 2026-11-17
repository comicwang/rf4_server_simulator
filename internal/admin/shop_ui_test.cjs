const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');
test('store summary and controls fit supported widths, DPI and long identifiers',async()=>{
 const html=fs.readFileSync(path.resolve(__dirname,'web/index.html'),'utf8');
 let store='fishingstore_009';
 const shelfCategories=[],listingGroups=[],listingCalls=[],customCreates=[],customUpdates=[],productUpdates=[];
 const server=http.createServer((req,res)=>{
  const pathname=new URL(req.url,'http://fixture').pathname.replace(/^\/admin\/api\//,'/api/');
  const json=value=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value));};
   if(pathname.startsWith('/admin/catalog-media/')){res.setHeader('Content-Type','image/png');res.end(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a84sAAAAASUVORK5CYII=','base64'));return;}
  if(pathname==='/api/me')return json({username:'admin'});
  if(pathname==='/api/stores')return json([{store_id:store,item_count:1,override_count:0},{store_id:'atm_006',item_count:2,override_count:0}]);
  if(pathname==='/api/stores/items'){shelfCategories.push(new URL(req.url,'http://fixture').searchParams.get('category'));const rows=[{store_id:store,item_id:'bait_001',name:'测试商品',category:2,base_silver:100,base_gold:1,base_stock:0,silver:100,gold:1,stock:1000,has_override:false}];if(customCreates.length)rows.push({store_id:store,item_id:'custom_test',name:'定制测试商品',category:27,base_silver:100,base_gold:0,base_stock:50,silver:100,gold:0,stock:50,has_override:true,listed:true});return json(rows);}
  if(pathname==='/api/catalog/groups')return json([{key:'bait',name:'饵料',count:1}]);
  if(pathname==='/api/catalog/items'){listingGroups.push(new URL(req.url,'http://fixture').searchParams.get('group'));return json([{item_id:'bait_001',name:'测试商品',group:'饵料'}]);}
  if(pathname==='/api/custom-products/base')return json({item_id:'bait_001',name:'测试商品',category:27,max_load_kg:5.123456,max_drag_kg:10,silver:100,gold:0,stock:50,parameters:[{path:'kngaaefkpka',label:'承力上限',kind:'float',value:'5.123456',bits:32},{path:'mlfjkaoicdn.omghfmdfjpc',label:'最大曳力',kind:'float',value:'10',bits:32},{path:'dhlkahnodoo',label:'重量',kind:'float',value:'33.2',bits:32}]});
  if(pathname==='/api/custom-products'&&req.method==='GET')return json({item_id:'custom_test',base_item_id:'bait_001',name:'定制测试商品',category:27,silver:100,gold:0,stock:50,parameters:[{path:'kngaaefkpka',label:'承力上限',kind:'float',value:'8.5',bits:32},{path:'mlfjkaoicdn.omghfmdfjpc',label:'最大曳力',kind:'float',value:'17.25',bits:32},{path:'dhlkahnodoo',label:'重量',kind:'float',value:'34.5',bits:32}]});
  if(pathname==='/api/stores/product')return json([{store_id:store,map_key:'map1',silver:100,gold:0,stock:1000},{store_id:'atm_006',map_key:'map2',silver:200,gold:0,stock:1000}]);
  if(pathname==='/api/stores/product/update'){let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{productUpdates.push(JSON.parse(body));json({ok:true,store_count:2});});return;}
  if(pathname==='/api/custom-products'&&req.method==='POST'){let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{customCreates.push(JSON.parse(body));json({ok:true,item_id:'custom_test',numeric_id:8102,base_item_id:'bait_001',name:'定制测试商品'});});return;}
  if(pathname==='/api/custom-products/update'&&req.method==='POST'){let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{customUpdates.push(JSON.parse(body));json({ok:true,item_id:'custom_test'});});return;}
  if(pathname==='/api/stores/items/list'&&req.method==='POST'){let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{listingCalls.push(JSON.parse(body));json({ok:true,store_count:JSON.parse(body).store_ids.length});});return;}
  if(pathname.startsWith('/api/'))return json({});
  res.setHeader('Content-Type','text/html; charset=utf-8');res.end(html);
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 let cases=0;
 try{
  for(const width of [1920,1024,360])for(const scale of [1,1.25,1.5]){
   const context=await browser.newContext({viewport:{width,height:900},deviceScaleFactor:scale});
   await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
   const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
   for(const value of ['fishingstore_009','最长合理商店标识中文'.repeat(4),'Store'.repeat(32)]){
    store=value;await page.goto('http://127.0.0.1:'+server.address().port+'/admin/',{waitUntil:'networkidle'});
    await page.evaluate(()=>go('stores'));await page.waitForFunction(()=>document.querySelector('#storeSummary').textContent.includes('补至 1000'));
    const m=await page.evaluate(()=>{const e=document.querySelector('#storeSummary'),r=e.getBoundingClientRect();return{font:getComputedStyle(e).fontSize,width:r.width,height:r.height,scroll:e.scrollWidth,available:e.clientWidth,page:document.documentElement.scrollWidth,viewport:innerWidth};});
    assert.ok(m.page<=m.viewport+2&&m.scroll<=m.available+1,JSON.stringify({width,scale,value,m}));
    for(const input of await page.locator('#storeTable input').all()){await input.scrollIntoViewIfNeeded();const r=await input.boundingBox();assert.ok(r.x>=0&&r.x+r.width<=width+2,JSON.stringify(r));}
    if(width===1920&&scale===1&&value==='fishingstore_009'){
     await page.locator('#storeCategory').selectOption('2');
     await page.waitForFunction(()=>document.querySelector('#storeCategory').value==='2');
     assert.ok(shelfCategories.includes('2'));
     await page.locator('#storeTable tbody tr').first().waitFor();
     assert.equal(await page.locator('#storeTable img').first().getAttribute('src'),'/admin/catalog-media/items/bait_001.webp');
     await page.locator('#view-stores button').filter({hasText:'上架新商品'}).click();
     const storeOption=await page.locator('#lsStoreOptions label').first().evaluate(label=>{const box=label.querySelector('input'),r=box.getBoundingClientRect();return{layout:getComputedStyle(label).display,width:r.width,height:r.height,textWidth:label.querySelector('span').getBoundingClientRect().width};});
     assert.equal(storeOption.layout,'grid');
     assert.ok(storeOption.width>=15&&storeOption.width<=17&&storeOption.height>=15&&storeOption.height<=17,JSON.stringify(storeOption));
     assert.ok(storeOption.textWidth>0,JSON.stringify(storeOption));
     await page.locator('#lsCategory option[value="bait"]').waitFor({state:'attached'});
     await page.locator('#lsCategory').selectOption('bait');
     await page.waitForFunction(()=>document.querySelector('#lsResults .result'));
     assert.equal(listingGroups.at(-1),'bait');
     await page.locator('#lsResults img').first().waitFor();
     assert.equal(await page.locator('#lsResults img').first().getAttribute('src'),'/admin/catalog-media/items/bait_001.webp');
     await page.locator('#lsStoreOptions input[value="atm_006"]').check();
     await page.locator('#lsResults .result').first().click();
     await page.locator('#lsSubmit').click();
     await page.waitForFunction(()=>!document.querySelector('#modalBg')?.classList.contains('on'));
     assert.equal(listingCalls.length,1);
     assert.deepEqual(listingCalls[0].store_ids.sort(),['atm_006','fishingstore_009'].sort());
     assert.equal(listingCalls[0].item_id,'bait_001');

     await page.locator('#view-stores button').filter({hasText:'定制商品'}).click();
     await page.locator('#cpCategory option[value="bait"]').waitFor({state:'attached'});
     await page.locator('#cpCategory').selectOption('bait');
     await page.locator('#cpResults .result').first().click();
     await page.locator('#cpEditor:not([hidden])').waitFor();
     await page.locator('#cpName').fill('定制测试商品');
     await page.locator('#cpMaxLoad').fill('8.5');
     await page.locator('#cpMaxDrag').fill('17.25');
     await page.locator('#cpParam2').fill('34.5');
     await page.locator('#cpCreate').click();
     await page.locator('#lsStoreOptions input[value="atm_006"]').waitFor();
     await page.waitForFunction(()=>document.querySelector('#lsPicked')?.textContent==='custom_test');
     assert.equal(customCreates.length,1);
     assert.deepEqual(customCreates[0],{base_item_id:'bait_001',name:'定制测试商品',parameters:{kngaaefkpka:'8.5','mlfjkaoicdn.omghfmdfjpc':'17.25',dhlkahnodoo:'34.5'}});
     await page.locator('#lsStoreOptions input[value="atm_006"]').check();
     await page.locator('#lsSubmit').click();
     await page.waitForFunction(()=>!document.querySelector('#modalBg')?.classList.contains('on'));
     assert.equal(listingCalls.length,2);
     assert.deepEqual(listingCalls[1].store_ids.sort(),['atm_006','fishingstore_009'].sort());
     assert.equal(listingCalls[1].item_id,'custom_test');

     await page.locator('#storeTable button').filter({hasText:'编辑属性'}).click();
     await page.locator('#cpEditor:not([hidden])').waitFor();
     await page.locator('#cpName').fill('新版定制商品');
     await page.locator('#cpParam2').fill('38');
     await page.locator('#cpCreate').click();
     await page.waitForFunction(()=>!document.querySelector('#modalBg')?.classList.contains('on'));
     assert.deepEqual(customUpdates[0],{item_id:'custom_test',name:'新版定制商品',parameters:{dhlkahnodoo:'38'}});

     await page.locator('#view-stores button').filter({hasText:'定制商品'}).click();
     await page.locator('#cpCategory option[value="bait"]').waitFor({state:'attached'});
     await page.locator('#cpCategory').selectOption('bait');
     await page.locator('#cpResults .result').first().click();
     await page.locator('#cpEditor:not([hidden])').waitFor();
     await page.locator('#cpMaxDrag').fill('20');
     await page.locator('#cpCreate').click();
     await page.waitForFunction(()=>document.querySelector('#lsPicked')?.textContent==='custom_test');
     assert.equal(customCreates.length,2);
     assert.equal(customCreates[1].parameters['mlfjkaoicdn.omghfmdfjpc'],'20');
     assert.equal(Object.hasOwn(customCreates[1].parameters,'kngaaefkpka'),false);
     await page.evaluate(()=>closeModal());
    }
    if(scale===1&&value==='fishingstore_009'){
     await page.locator('#storeTable button').filter({hasText:'全店更新'}).first().click();
     await page.locator('#spEditor:not([hidden])').waitFor();
     assert.equal(await page.locator('#spStores tr').count(),2);
     assert.equal(await page.locator('#spSilver').inputValue(),'');
     await page.locator('#spSilver').fill('789');
     await page.locator('#spStock').fill('45');
     const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+2);
     assert.equal(overflow,false,'bulk editor page must fit viewport');
     if(width===360)await page.screenshot({path:'output/playwright/product-update-mobile.png',fullPage:true});
     if(width===1920)await page.screenshot({path:'output/playwright/product-update-desktop.png',fullPage:true});
     await page.locator('#spSubmit').click();
     await page.waitForFunction(()=>!document.querySelector('#modalBg')?.classList.contains('on'));
     assert.deepEqual(productUpdates.at(-1),{item_id:'bait_001',silver:789,gold:0,stock:45});
    }
    assert.deepEqual(errors,[]);if(width===1920&&scale===1&&value==='fishingstore_009')console.log(JSON.stringify({width,scale,...m}));cases++;
   }
   await context.close();
  }
  console.log(JSON.stringify({storeRenderCases:cases,widths:[1920,1024,360],dpi:[1,1.25,1.5],textCases:3}));
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
});
