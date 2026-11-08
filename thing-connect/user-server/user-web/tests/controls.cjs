// Exercise Ant Design Select through the same visible controls as a user.
async function choose(control, label) {
  if(await control.getAttribute('aria-expanded') !== 'true') await control.locator('xpath=ancestor::*[contains(concat(" ",normalize-space(@class)," ")," ant-select ")][1]').locator('.ant-select-selector').click();
  await control.page().getByRole('option', {name:label,exact:true}).click();
  await control.page().locator('.ant-select-dropdown:visible').waitFor({state:'hidden'});
}
async function selectedLabel(control) {
  return control.locator('xpath=ancestor::*[contains(concat(" ",normalize-space(@class)," ")," ant-select ")][1]').locator('.ant-select-selection-item').innerText();
}
module.exports={choose,selectedLabel};
