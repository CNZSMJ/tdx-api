# Professional Finance Field Catalog Baseline

## Scope

- Source system: `gpcw` professional finance files (`FINVALUE` / `FINONE`)
- This appendix is normative for the professional finance API field universe.
- `FINVALUE` and `FINONE` share the same `source_field_id` definitions. `FINVALUE` is report-series oriented; `FINONE` is point-in-time/date-parameterized access on the same field set.
- This appendix covers only professional finance fields. It excludes `GPJYVALUE`, `BKJYVALUE`, `SCJYVALUE`, and `GPONEDAT`.

## Full Field Universe

Completed normalization batches are recorded as tables with `source_field_id`, `field_code`, `field_name_cn`, `field_name_en`, and `category`.
`field_code` is unique within this appendix. If multiple sections contain near-identical concepts, the baseline gives the canonical unsuffixed `field_code` to the base per-share or primary statement field. Non-canonical variants use category-style suffixes, and if a same-category duplicate still needs to be preserved, it uses an extra qualifier before the category suffix.
Row-level `category` follows public field-catalog classification by professional financial data semantics, not the source appendix section heading.
For earnings preview and earnings flash report fields, `category` follows information-source semantics first. These fields should be grouped under dedicated preview/flash categories even when the underlying economic meaning resembles balance-sheet, income-statement, per-share, or profitability fields.

### 元数据 (`0-0`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `0` | `report_date` | `返回报告期` | `Report Date` | `meta` |

### 每股指标 (`1-7`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `1` | `basic_earnings_per_share` | `基本每股收益` | `Basic Earnings per Share` | `per_share` |
| `2` | `basic_earnings_per_share_after_deducting_non_recurring_profit_and_loss` | `扣除非经常性损益每股收益` | `Basic Earnings per Share after Deducting Non-recurring Profit and Loss` | `per_share` |
| `3` | `undistributed_profit_per_share` | `每股未分配利润` | `Undistributed Profit per Share` | `per_share` |
| `4` | `book_value_per_share` | `每股净资产` | `Book Value per Share` | `per_share` |
| `5` | `capital_reserve_per_share` | `每股资本公积金` | `Capital Reserve per Share` | `per_share` |
| `6` | `roe` | `净资产收益率` | `Return on Equity` | `profitability` |
| `7` | `operating_cash_flow_per_share` | `每股经营现金流量` | `Operating Cash Flow per Share` | `per_share` |

### 资产负债表 (`8-73`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `8` | `cash_and_cash_equivalents` | `货币资金` | `Cash and Cash Equivalents` | `balance_sheet` |
| `9` | `trading_financial_assets` | `交易性金融资产` | `Trading Financial Assets` | `balance_sheet` |
| `10` | `notes_receivable` | `应收票据` | `Notes Receivable` | `balance_sheet` |
| `11` | `accounts_receivable` | `应收账款` | `Accounts Receivable` | `balance_sheet` |
| `12` | `prepayments` | `预付款项` | `Prepayments` | `balance_sheet` |
| `13` | `other_receivables` | `其他应收款` | `Other Receivables` | `balance_sheet` |
| `14` | `due_from_related_companies` | `应收关联公司款` | `Amounts Due from Related Companies` | `balance_sheet` |
| `15` | `interest_receivable` | `应收利息` | `Interest Receivable` | `balance_sheet` |
| `16` | `dividends_receivable` | `应收股利` | `Dividends Receivable` | `balance_sheet` |
| `17` | `inventories` | `存货` | `Inventories` | `balance_sheet` |
| `18` | `consumable_biological_assets` | `其中：消耗性生物资产` | `Consumable Biological Assets` | `balance_sheet` |
| `19` | `non_current_assets_due_within_one_year` | `一年内到期的非流动资产` | `Non-current Assets Due within One Year` | `balance_sheet` |
| `20` | `other_current_assets` | `其他流动资产` | `Other Current Assets` | `balance_sheet` |
| `21` | `total_current_assets` | `流动资产合计` | `Total Current Assets` | `balance_sheet` |
| `22` | `available_for_sale_financial_assets` | `可供出售金融资产` | `Available-for-sale Financial Assets` | `balance_sheet` |
| `23` | `held_to_maturity_investments` | `持有至到期投资` | `Held-to-maturity Investments` | `balance_sheet` |
| `24` | `long_term_receivables` | `长期应收款` | `Long-term Receivables` | `balance_sheet` |
| `25` | `long_term_equity_investments` | `长期股权投资` | `Long-term Equity Investments` | `balance_sheet` |
| `26` | `investment_properties` | `投资性房地产` | `Investment Properties` | `balance_sheet` |
| `27` | `fixed_assets` | `固定资产` | `Fixed Assets` | `balance_sheet` |
| `28` | `construction_in_progress` | `在建工程` | `Construction in Progress` | `balance_sheet` |
| `29` | `construction_materials` | `工程物资` | `Construction Materials` | `balance_sheet` |
| `30` | `fixed_assets_pending_disposal` | `固定资产清理` | `Fixed Assets Pending Disposal` | `balance_sheet` |
| `31` | `productive_biological_assets` | `生产性生物资产` | `Productive Biological Assets` | `balance_sheet` |
| `32` | `oil_and_gas_assets` | `油气资产` | `Oil and Gas Assets` | `balance_sheet` |
| `33` | `intangible_assets` | `无形资产` | `Intangible Assets` | `balance_sheet` |
| `34` | `development_expenditure` | `开发支出` | `Development Expenditure` | `balance_sheet` |
| `35` | `goodwill` | `商誉` | `Goodwill` | `balance_sheet` |
| `36` | `long_term_deferred_expenses` | `长期待摊费用` | `Long-term Deferred Expenses` | `balance_sheet` |
| `37` | `deferred_tax_assets` | `递延所得税资产` | `Deferred Tax Assets` | `balance_sheet` |
| `38` | `other_non_current_assets` | `其他非流动资产` | `Other Non-current Assets` | `balance_sheet` |
| `39` | `total_non_current_assets` | `非流动资产合计` | `Total Non-current Assets` | `balance_sheet` |
| `40` | `total_assets` | `资产总计` | `Total Assets` | `balance_sheet` |
| `41` | `short_term_borrowings` | `短期借款` | `Short-term Borrowings` | `balance_sheet` |
| `42` | `trading_financial_liabilities` | `交易性金融负债` | `Trading Financial Liabilities` | `balance_sheet` |
| `43` | `notes_payable` | `应付票据` | `Notes Payable` | `balance_sheet` |
| `44` | `accounts_payable` | `应付账款` | `Accounts Payable` | `balance_sheet` |
| `45` | `advances_from_customers` | `预收款项` | `Advances from Customers` | `balance_sheet` |
| `46` | `employee_compensation_payable` | `应付职工薪酬` | `Employee Compensation Payable` | `balance_sheet` |
| `47` | `taxes_payable` | `应交税费` | `Taxes Payable` | `balance_sheet` |
| `48` | `interest_payable` | `应付利息` | `Interest Payable` | `balance_sheet` |
| `49` | `dividends_payable` | `应付股利` | `Dividends Payable` | `balance_sheet` |
| `50` | `other_payables` | `其他应付款` | `Other Payables` | `balance_sheet` |
| `51` | `due_to_related_companies` | `应付关联公司款` | `Amounts Due to Related Companies` | `balance_sheet` |
| `52` | `non_current_liabilities_due_within_one_year` | `一年内到期的非流动负债` | `Non-current Liabilities Due within One Year` | `balance_sheet` |
| `53` | `other_current_liabilities` | `其他流动负债` | `Other Current Liabilities` | `balance_sheet` |
| `54` | `total_current_liabilities` | `流动负债合计` | `Total Current Liabilities` | `balance_sheet` |
| `55` | `long_term_borrowings` | `长期借款` | `Long-term Borrowings` | `balance_sheet` |
| `56` | `bonds_payable` | `应付债券` | `Bonds Payable` | `balance_sheet` |
| `57` | `long_term_payables` | `长期应付款` | `Long-term Payables` | `balance_sheet` |
| `58` | `special_payables` | `专项应付款` | `Special Payables` | `balance_sheet` |
| `59` | `provisions` | `预计负债` | `Provisions` | `balance_sheet` |
| `60` | `deferred_tax_liabilities` | `递延所得税负债` | `Deferred Tax Liabilities` | `balance_sheet` |
| `61` | `other_non_current_liabilities` | `其他非流动负债` | `Other Non-current Liabilities` | `balance_sheet` |
| `62` | `total_non_current_liabilities` | `非流动负债合计` | `Total Non-current Liabilities` | `balance_sheet` |
| `63` | `total_liabilities` | `负债合计` | `Total Liabilities` | `balance_sheet` |
| `64` | `share_capital` | `实收资本（或股本）` | `Share Capital` | `balance_sheet` |
| `65` | `capital_reserve` | `资本公积` | `Capital Reserve` | `balance_sheet` |
| `66` | `surplus_reserve` | `盈余公积` | `Surplus Reserve` | `balance_sheet` |
| `67` | `treasury_shares` | `减：库存股` | `Treasury Shares` | `balance_sheet` |
| `68` | `retained_earnings` | `未分配利润` | `Retained Earnings` | `balance_sheet` |
| `69` | `non_controlling_interests` | `少数股东权益` | `Non-controlling Interests` | `balance_sheet` |
| `70` | `foreign_currency_translation_differences` | `外币报表折算价差` | `Foreign Currency Translation Differences` | `balance_sheet` |
| `71` | `adjustments_to_income_from_abnormal_operations` | `非正常经营项目收益调整` | `Adjustments to Income from Abnormal Operations` | `balance_sheet` |
| `72` | `total_equity` | `所有者权益（或股东权益）合计` | `Total Equity` | `balance_sheet` |
| `73` | `total_liabilities_and_equity` | `负债和所有者（或股东权益）合计` | `Total Liabilities and Equity` | `balance_sheet` |

### 利润表 (`74-97`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `74` | `operating_revenue` | `其中：营业收入` | `Operating Revenue` | `income_statement` |
| `75` | `operating_costs` | `其中：营业成本` | `Operating Costs` | `income_statement` |
| `76` | `taxes_and_surcharges` | `营业税金及附加` | `Taxes and Surcharges` | `income_statement` |
| `77` | `selling_expenses` | `销售费用` | `Selling Expenses` | `income_statement` |
| `78` | `administrative_expenses` | `管理费用` | `Administrative Expenses` | `income_statement` |
| `79` | `exploration_expenses` | `勘探费用` | `Exploration Expenses` | `income_statement` |
| `80` | `finance_expenses` | `财务费用` | `Finance Expenses` | `income_statement` |
| `81` | `asset_impairment_losses` | `资产减值损失` | `Asset Impairment Losses` | `income_statement` |
| `82` | `net_gain_from_fair_value_changes` | `加：公允价值变动净收益` | `Net Gain from Fair Value Changes` | `income_statement` |
| `83` | `investment_income` | `投资收益` | `Investment Income` | `income_statement` |
| `84` | `investment_income_from_associates_and_joint_ventures` | `其中：对联营企业和合营企业的投资收益` | `Investment Income from Associates and Joint Ventures` | `income_statement` |
| `85` | `other_items_affecting_operating_profit` | `影响营业利润的其他科目` | `Other Items Affecting Operating Profit` | `income_statement` |
| `86` | `operating_profit` | `三、营业利润` | `Operating Profit` | `income_statement` |
| `87` | `subsidy_income` | `加：补贴收入` | `Subsidy Income` | `income_statement` |
| `88` | `non_operating_income` | `营业外收入` | `Non-operating Income` | `income_statement` |
| `89` | `non_operating_expenses` | `减：营业外支出` | `Non-operating Expenses` | `income_statement` |
| `90` | `net_loss_from_disposal_of_non_current_assets` | `其中：非流动资产处置净损失` | `Net Loss from Disposal of Non-current Assets` | `income_statement` |
| `91` | `other_items_affecting_total_profit` | `加：影响利润总额的其他科目` | `Other Items Affecting Total Profit` | `income_statement` |
| `92` | `total_profit` | `四、利润总额` | `Total Profit` | `income_statement` |
| `93` | `income_tax_expense` | `减：所得税` | `Income Tax Expense` | `income_statement` |
| `94` | `other_items_affecting_net_profit` | `加：影响净利润的其他科目` | `Other Items Affecting Net Profit` | `income_statement` |
| `95` | `net_profit` | `五、净利润` | `Net Profit` | `income_statement` |
| `96` | `net_profit_attributable_to_owners_of_parent` | `归属于母公司所有者的净利润` | `Net Profit Attributable to Owners of the Parent` | `income_statement` |
| `97` | `profit_attributable_to_non_controlling_interests` | `少数股东损益` | `Profit Attributable to Non-controlling Interests` | `income_statement` |

### 现金流量表 (`98-158`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `98` | `cash_received_from_sale_of_goods_and_rendering_of_services` | `销售商品、提供劳务收到的现金` | `Cash Received from Sale of Goods and Rendering of Services` | `cash_flow_statement` |
| `99` | `tax_refunds_received` | `收到的税费返还` | `Tax Refunds Received` | `cash_flow_statement` |
| `100` | `other_cash_received_relating_to_operating_activities` | `收到其他与经营活动有关的现金` | `Other Cash Received Relating to Operating Activities` | `cash_flow_statement` |
| `101` | `subtotal_of_cash_inflows_from_operating_activities` | `经营活动现金流入小计` | `Subtotal of Cash Inflows from Operating Activities` | `cash_flow_statement` |
| `102` | `cash_paid_for_goods_and_services` | `购买商品、接受劳务支付的现金` | `Cash Paid for Goods and Services` | `cash_flow_statement` |
| `103` | `cash_paid_to_and_for_employees` | `支付给职工以及为职工支付的现金` | `Cash Paid to and for Employees` | `cash_flow_statement` |
| `104` | `cash_paid_for_taxes` | `支付的各项税费` | `Cash Paid for Taxes` | `cash_flow_statement` |
| `105` | `other_cash_paid_relating_to_operating_activities` | `支付其他与经营活动有关的现金` | `Other Cash Paid Relating to Operating Activities` | `cash_flow_statement` |
| `106` | `subtotal_of_cash_outflows_from_operating_activities` | `经营活动现金流出小计` | `Subtotal of Cash Outflows from Operating Activities` | `cash_flow_statement` |
| `107` | `net_cash_flow_from_operating_activities` | `经营活动产生的现金流量净额` | `Net Cash Flow from Operating Activities` | `cash_flow_statement` |
| `108` | `cash_received_from_recovery_of_investments` | `收回投资收到的现金` | `Cash Received from Recovery of Investments` | `cash_flow_statement` |
| `109` | `cash_received_from_investment_income` | `取得投资收益收到的现金` | `Cash Received from Investment Income` | `cash_flow_statement` |
| `110` | `net_cash_received_from_disposal_of_fixed_assets_intangible_assets_and_other_long_term_assets` | `处置固定资产、无形资产和其他长期资产收回的现金净额` | `Net Cash Received from Disposal of Fixed Assets, Intangible Assets and Other Long-term Assets` | `cash_flow_statement` |
| `111` | `net_cash_received_from_disposal_of_subsidiaries_and_other_business_units` | `处置子公司及其他营业单位收到的现金净额` | `Net Cash Received from Disposal of Subsidiaries and Other Business Units` | `cash_flow_statement` |
| `112` | `other_cash_received_relating_to_investing_activities` | `收到其他与投资活动有关的现金` | `Other Cash Received Relating to Investing Activities` | `cash_flow_statement` |
| `113` | `subtotal_of_cash_inflows_from_investing_activities` | `投资活动现金流入小计` | `Subtotal of Cash Inflows from Investing Activities` | `cash_flow_statement` |
| `114` | `cash_paid_for_acquisition_of_fixed_assets_intangible_assets_and_other_long_term_assets` | `购建固定资产、无形资产和其他长期资产支付的现金` | `Cash Paid for Acquisition of Fixed Assets, Intangible Assets and Other Long-term Assets` | `cash_flow_statement` |
| `115` | `cash_paid_for_investments` | `投资支付的现金` | `Cash Paid for Investments` | `cash_flow_statement` |
| `116` | `net_cash_paid_for_acquisition_of_subsidiaries_and_other_business_units` | `取得子公司及其他营业单位支付的现金净额` | `Net Cash Paid for Acquisition of Subsidiaries and Other Business Units` | `cash_flow_statement` |
| `117` | `other_cash_paid_relating_to_investing_activities` | `支付其他与投资活动有关的现金` | `Other Cash Paid Relating to Investing Activities` | `cash_flow_statement` |
| `118` | `subtotal_of_cash_outflows_from_investing_activities` | `投资活动现金流出小计` | `Subtotal of Cash Outflows from Investing Activities` | `cash_flow_statement` |
| `119` | `net_cash_flow_from_investing_activities` | `投资活动产生的现金流量净额` | `Net Cash Flow from Investing Activities` | `cash_flow_statement` |
| `120` | `cash_received_from_capital_contributions` | `吸收投资收到的现金` | `Cash Received from Capital Contributions` | `cash_flow_statement` |
| `121` | `cash_received_from_borrowings` | `取得借款收到的现金` | `Cash Received from Borrowings` | `cash_flow_statement` |
| `122` | `other_cash_received_relating_to_financing_activities` | `收到其他与筹资活动有关的现金` | `Other Cash Received Relating to Financing Activities` | `cash_flow_statement` |
| `123` | `subtotal_of_cash_inflows_from_financing_activities` | `筹资活动现金流入小计` | `Subtotal of Cash Inflows from Financing Activities` | `cash_flow_statement` |
| `124` | `cash_paid_for_repayment_of_debt` | `偿还债务支付的现金` | `Cash Paid for Repayment of Debt` | `cash_flow_statement` |
| `125` | `cash_paid_for_dividends_profit_distribution_or_interest` | `分配股利、利润或偿付利息支付的现金` | `Cash Paid for Dividends, Profit Distribution or Interest` | `cash_flow_statement` |
| `126` | `other_cash_paid_relating_to_financing_activities` | `支付其他与筹资活动有关的现金` | `Other Cash Paid Relating to Financing Activities` | `cash_flow_statement` |
| `127` | `subtotal_of_cash_outflows_from_financing_activities` | `筹资活动现金流出小计` | `Subtotal of Cash Outflows from Financing Activities` | `cash_flow_statement` |
| `128` | `net_cash_flow_from_financing_activities` | `筹资活动产生的现金流量净额` | `Net Cash Flow from Financing Activities` | `cash_flow_statement` |
| `129` | `effect_of_exchange_rate_changes_on_cash` | `四、汇率变动对现金的影响` | `Effect of Exchange Rate Changes on Cash` | `cash_flow_statement` |
| `130` | `effect_of_other_reasons_on_cash` | `四(2)、其他原因对现金的影响` | `Effect of Other Reasons on Cash` | `cash_flow_statement` |
| `131` | `net_increase_in_cash_and_cash_equivalents` | `五、现金及现金等价物净增加额` | `Net Increase in Cash and Cash Equivalents` | `cash_flow_statement` |
| `132` | `opening_balance_of_cash_and_cash_equivalents` | `期初现金及现金等价物余额` | `Opening Balance of Cash and Cash Equivalents` | `cash_flow_statement` |
| `133` | `closing_balance_of_cash_and_cash_equivalents` | `期末现金及现金等价物余额` | `Closing Balance of Cash and Cash Equivalents` | `cash_flow_statement` |
| `134` | `net_profit_for_cash_flow_reconciliation` | `净利润` | `Net Profit for Cash Flow Reconciliation` | `cash_flow_statement` |
| `135` | `asset_impairment_provisions` | `加：资产减值准备` | `Asset Impairment Provisions` | `cash_flow_statement` |
| `136` | `depreciation_of_fixed_assets_depletion_of_oil_and_gas_assets_and_depreciation_of_productive_biological_assets` | `固定资产折旧、油气资产折耗、生产性生物资产折旧` | `Depreciation of Fixed Assets, Depletion of Oil and Gas Assets and Depreciation of Productive Biological Assets` | `cash_flow_statement` |
| `137` | `amortization_of_intangible_assets` | `无形资产摊销` | `Amortization of Intangible Assets` | `cash_flow_statement` |
| `138` | `amortization_of_long_term_deferred_expenses` | `长期待摊费用摊销` | `Amortization of Long-term Deferred Expenses` | `cash_flow_statement` |
| `139` | `loss_on_disposal_of_fixed_assets_intangible_assets_and_other_long_term_assets` | `处置固定资产、无形资产和其他长期资产的损失` | `Loss on Disposal of Fixed Assets, Intangible Assets and Other Long-term Assets` | `cash_flow_statement` |
| `140` | `loss_on_scrapping_of_fixed_assets` | `固定资产报废损失` | `Loss on Scrapping of Fixed Assets` | `cash_flow_statement` |
| `141` | `loss_from_fair_value_changes` | `公允价值变动损失` | `Loss from Fair Value Changes` | `cash_flow_statement` |
| `142` | `finance_expenses_for_cash_flow_reconciliation` | `财务费用` | `Finance Expenses for Cash Flow Reconciliation` | `cash_flow_statement` |
| `143` | `investment_losses` | `投资损失` | `Investment Losses` | `cash_flow_statement` |
| `144` | `decrease_in_deferred_tax_assets` | `递延所得税资产减少` | `Decrease in Deferred Tax Assets` | `cash_flow_statement` |
| `145` | `increase_in_deferred_tax_liabilities` | `递延所得税负债增加` | `Increase in Deferred Tax Liabilities` | `cash_flow_statement` |
| `146` | `decrease_in_inventories` | `存货的减少` | `Decrease in Inventories` | `cash_flow_statement` |
| `147` | `decrease_in_operating_receivables` | `经营性应收项目的减少` | `Decrease in Operating Receivables` | `cash_flow_statement` |
| `148` | `increase_in_operating_payables` | `经营性应付项目的增加` | `Increase in Operating Payables` | `cash_flow_statement` |
| `149` | `other_cash_flow_reconciliation_items` | `其他` | `Other Cash Flow Reconciliation Items` | `cash_flow_statement` |
| `150` | `net_cash_flow_from_operating_activities_indirect_method` | `经营活动产生的现金流量净额2` | `Net Cash Flow from Operating Activities under the Indirect Method` | `cash_flow_statement` |
| `151` | `debt_converted_into_capital` | `债务转为资本` | `Debt Converted into Capital` | `cash_flow_statement` |
| `152` | `convertible_bonds_due_within_one_year` | `一年内到期的可转换公司债券` | `Convertible Bonds Due within One Year` | `cash_flow_statement` |
| `153` | `fixed_assets_acquired_under_finance_leases` | `融资租入固定资产` | `Fixed Assets Acquired under Finance Leases` | `cash_flow_statement` |
| `154` | `closing_cash_balance` | `现金的期末余额` | `Closing Cash Balance` | `cash_flow_statement` |
| `155` | `opening_cash_balance` | `减：现金的期初余额` | `Opening Cash Balance` | `cash_flow_statement` |
| `156` | `closing_cash_equivalents_balance` | `加：现金等价物的期末余额` | `Closing Cash Equivalents Balance` | `cash_flow_statement` |
| `157` | `opening_cash_equivalents_balance` | `减：现金等价物的期初余额` | `Opening Cash Equivalents Balance` | `cash_flow_statement` |
| `158` | `net_increase_in_cash_and_cash_equivalents_reconciliation` | `现金及现金等价物净增加额` | `Net Increase in Cash and Cash Equivalents from Reconciliation` | `cash_flow_statement` |

### 偿债能力分析 (`159-171`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `159` | `current_ratio` | `流动比率(非金融类指标)` | `Current Ratio` | `solvency` |
| `160` | `quick_ratio` | `速动比率(非金融类指标)` | `Quick Ratio` | `solvency` |
| `161` | `cash_ratio` | `现金比率(%)(非金融类指标)` | `Cash Ratio` | `solvency` |
| `162` | `interest_coverage_ratio` | `利息保障倍数(非金融类指标)` | `Interest Coverage Ratio` | `solvency` |
| `163` | `non_current_liabilities_ratio` | `非流动负债比率(%)(非金融类指标)` | `Non-current Liabilities Ratio` | `solvency` |
| `164` | `current_liabilities_ratio` | `流动负债比率(%)(非金融类指标)` | `Current Liabilities Ratio` | `solvency` |
| `165` | `cash_to_maturing_debt_ratio` | `现金到期债务比率(%)(非金融类指标)` | `Cash to Maturing Debt Ratio` | `solvency` |
| `166` | `debt_to_tangible_net_worth_ratio` | `有形资产净值债务率(%)` | `Debt to Tangible Net Worth Ratio` | `solvency` |
| `167` | `equity_multiplier` | `权益乘数(%)` | `Equity Multiplier` | `solvency` |
| `168` | `equity_to_total_liabilities_ratio` | `股东的权益/负债合计(%)` | `Equity to Total Liabilities Ratio` | `solvency` |
| `169` | `tangible_assets_to_total_liabilities_ratio` | `有形资产/负债合计(%)` | `Tangible Assets to Total Liabilities Ratio` | `solvency` |
| `170` | `net_cash_flow_from_operating_activities_to_total_liabilities_ratio` | `经营活动产生的现金流量净额/负债合计(%)(非金融类指标)` | `Net Cash Flow from Operating Activities to Total Liabilities Ratio` | `solvency` |
| `171` | `ebitda_to_total_liabilities_ratio` | `EBITDA/负债合计(%)(非金融类指标)` | `EBITDA to Total Liabilities Ratio` | `solvency` |

### 经营效率分析 (`172-182`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `172` | `accounts_receivable_turnover_ratio` | `应收帐款周转率(非金融类指标)` | `Accounts Receivable Turnover Ratio` | `operating_efficiency` |
| `173` | `inventory_turnover_ratio` | `存货周转率(非金融类指标)` | `Inventory Turnover Ratio` | `operating_efficiency` |
| `174` | `working_capital_turnover_ratio` | `运营资金周转率(非金融类指标)` | `Working Capital Turnover Ratio` | `operating_efficiency` |
| `175` | `total_asset_turnover_ratio` | `总资产周转率(非金融类指标)` | `Total Asset Turnover Ratio` | `operating_efficiency` |
| `176` | `fixed_asset_turnover_ratio` | `固定资产周转率(非金融类指标)` | `Fixed Asset Turnover Ratio` | `operating_efficiency` |
| `177` | `accounts_receivable_turnover_days` | `应收帐款周转天数(非金融类指标)` | `Accounts Receivable Turnover Days` | `operating_efficiency` |
| `178` | `inventory_turnover_days` | `存货周转天数(非金融类指标)` | `Inventory Turnover Days` | `operating_efficiency` |
| `179` | `current_asset_turnover_ratio` | `流动资产周转率(非金融类指标)` | `Current Asset Turnover Ratio` | `operating_efficiency` |
| `180` | `current_asset_turnover_days` | `流动资产周转天数(非金融类指标)` | `Current Asset Turnover Days` | `operating_efficiency` |
| `181` | `total_asset_turnover_days` | `总资产周转天数(非金融类指标)` | `Total Asset Turnover Days` | `operating_efficiency` |
| `182` | `equity_turnover_ratio` | `股东权益周转率(非金融类指标)` | `Equity Turnover Ratio` | `operating_efficiency` |

### 发展能力分析 (`183-192`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `183` | `operating_revenue_growth_rate` | `营业收入增长率(%)` | `Operating Revenue Growth Rate` | `growth` |
| `184` | `net_profit_growth_rate` | `净利润增长率(%)` | `Net Profit Growth Rate` | `growth` |
| `185` | `net_asset_growth_rate` | `净资产增长率(%)` | `Net Asset Growth Rate` | `growth` |
| `186` | `fixed_asset_growth_rate` | `固定资产增长率(%)` | `Fixed Asset Growth Rate` | `growth` |
| `187` | `total_asset_growth_rate` | `总资产增长率(%)` | `Total Asset Growth Rate` | `growth` |
| `188` | `investment_income_growth_rate` | `投资收益增长率(%)` | `Investment Income Growth Rate` | `growth` |
| `189` | `operating_profit_growth_rate` | `营业利润增长率(%)` | `Operating Profit Growth Rate` | `growth` |
| `190` | `basic_earnings_per_share_after_deducting_non_recurring_profit_and_loss_growth_rate` | `扣非每股收益同比(%)` | `Growth Rate of Basic Earnings per Share after Deducting Non-recurring Profit and Loss` | `growth` |
| `191` | `net_profit_after_deducting_non_recurring_profit_and_loss_growth_rate` | `扣非净利润同比(%)` | `Growth Rate of Net Profit after Deducting Non-recurring Profit and Loss` | `growth` |

| `192` | `reserved_growth_metric_192` | `暂无` | `Reserved Growth Metric 192` | `growth` |

### 获利能力分析 (`193-209`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `193` | `profit_to_cost_and_expense_ratio` | `成本费用利润率(%)` | `Profit to Cost and Expense Ratio` | `profitability` |
| `194` | `operating_profit_margin` | `营业利润率(非金融类指标)` | `Operating Profit Margin` | `profitability` |
| `195` | `taxes_and_surcharges_ratio` | `营业税金率(非金融类指标)` | `Taxes and Surcharges Ratio` | `profitability` |
| `196` | `operating_cost_ratio` | `营业成本率(非金融类指标)` | `Operating Cost Ratio` | `profitability` |
| `197` | `roe_profitability` | `净资产收益率` | `Return on Equity (Profitability Variant)` | `profitability` |
| `198` | `return_on_investment` | `投资收益率` | `Return on Investment` | `profitability` |
| `199` | `sales_net_profit_margin` | `销售净利率(%)` | `Sales Net Profit Margin` | `profitability` |
| `200` | `return_on_total_assets` | `总资产净利率` | `Return on Total Assets` | `profitability` |
| `201` | `net_profit_margin` | `净利润率(非金融类指标)` | `Net Profit Margin` | `profitability` |
| `202` | `gross_profit_margin` | `销售毛利率(%)(非金融类指标)` | `Gross Profit Margin` | `profitability` |
| `203` | `selling_administrative_and_finance_expense_ratio` | `三费比重(非金融类指标)` | `Selling, Administrative and Finance Expense Ratio` | `profitability` |
| `204` | `administrative_expense_ratio` | `管理费用率(非金融类指标)` | `Administrative Expense Ratio` | `profitability` |
| `205` | `finance_expense_ratio` | `财务费用率(非金融类指标)` | `Finance Expense Ratio` | `profitability` |
| `206` | `net_profit_after_deducting_non_recurring_profit_and_loss` | `扣除非经常性损益后的净利润` | `Net Profit after Deducting Non-recurring Profit and Loss` | `profitability` |
| `207` | `ebit` | `息税前利润(EBIT)` | `Earnings Before Interest and Taxes` | `profitability` |
| `208` | `ebitda` | `息税折旧摊销前利润(EBITDA)` | `Earnings Before Interest, Taxes, Depreciation and Amortization` | `profitability` |
| `209` | `ebitda_to_total_operating_revenue_ratio` | `EBITDA/营业总收入(%)(非金融类指标)` | `EBITDA to Total Operating Revenue Ratio` | `profitability` |

### 资本结构分析 (`210-218`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `210` | `debt_to_asset_ratio` | `资产负债率(%)` | `Debt-to-Asset Ratio` | `capital_structure` |
| `211` | `current_assets_to_total_assets_ratio` | `流动资产比率(非金融类指标)` | `Current Assets to Total Assets Ratio` | `capital_structure` |
| `212` | `cash_and_cash_equivalents_to_total_assets_ratio` | `货币资金比率(非金融类指标)` | `Cash and Cash Equivalents to Total Assets Ratio` | `capital_structure` |
| `213` | `inventory_to_total_assets_ratio` | `存货比率(非金融类指标)` | `Inventory to Total Assets Ratio` | `capital_structure` |
| `214` | `fixed_assets_to_total_assets_ratio` | `固定资产比率` | `Fixed Assets to Total Assets Ratio` | `capital_structure` |
| `215` | `long_term_liabilities_to_total_liabilities_ratio` | `负债结构比(非金融类指标)` | `Long-term Liabilities to Total Liabilities Ratio` | `capital_structure` |
| `216` | `equity_attributable_to_owners_of_parent_to_total_invested_capital_ratio` | `归属于母公司股东权益/全部投入资本(%)` | `Equity Attributable to Owners of the Parent to Total Invested Capital Ratio` | `capital_structure` |
| `217` | `equity_to_interest_bearing_debt_ratio` | `股东的权益/带息债务(%)` | `Equity to Interest-bearing Debt Ratio` | `capital_structure` |
| `218` | `tangible_assets_to_net_debt_ratio` | `有形资产/净债务(%)` | `Tangible Assets to Net Debt Ratio` | `capital_structure` |

### 现金流量分析 (`219-229`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `219` | `operating_cash_flow_per_share_cash_flow_analysis` | `每股经营性现金流(元)` | `Operating Cash Flow per Share (Cash Flow Analysis Variant)` | `cash_flow_analysis` |
| `220` | `cash_content_of_operating_revenue` | `营业收入现金含量(%)(非金融类指标)` | `Cash Content of Operating Revenue` | `cash_flow_analysis` |
| `221` | `net_cash_flow_from_operating_activities_to_net_operating_income_ratio` | `经营活动产生的现金流量净额/经营活动净收益(%)` | `Net Cash Flow from Operating Activities to Net Operating Income Ratio` | `cash_flow_analysis` |
| `222` | `cash_received_from_goods_sales_and_services_to_operating_revenue_ratio` | `销售商品提供劳务收到的现金/营业收入(%)` | `Cash Received from Sale of Goods and Rendering of Services to Operating Revenue Ratio` | `cash_flow_analysis` |
| `223` | `net_cash_flow_from_operating_activities_to_operating_revenue_ratio` | `经营活动产生的现金流量净额/营业收入` | `Net Cash Flow from Operating Activities to Operating Revenue Ratio` | `cash_flow_analysis` |
| `224` | `capital_expenditure_to_depreciation_and_amortization_ratio` | `资本支出/折旧和摊销` | `Capital Expenditure to Depreciation and Amortization Ratio` | `cash_flow_analysis` |
| `225` | `net_increase_in_cash_and_cash_equivalents_per_share` | `每股现金流量净额(元)` | `Net Increase in Cash and Cash Equivalents per Share` | `cash_flow_analysis` |
| `226` | `net_cash_flow_from_operating_activities_to_short_term_debt_ratio` | `经营净现金比率（短期债务）(非金融类指标)` | `Net Cash Flow from Operating Activities to Short-term Debt Ratio` | `cash_flow_analysis` |
| `227` | `net_cash_flow_from_operating_activities_to_total_debt_ratio` | `经营净现金比率（全部债务）` | `Net Cash Flow from Operating Activities to Total Debt Ratio` | `cash_flow_analysis` |
| `228` | `net_cash_flow_from_operating_activities_to_net_profit_ratio` | `经营活动现金净流量与净利润比率` | `Net Cash Flow from Operating Activities to Net Profit Ratio` | `cash_flow_analysis` |
| `229` | `cash_recovery_ratio_of_total_assets` | `全部资产现金回收率` | `Cash Recovery Ratio of Total Assets` | `cash_flow_analysis` |

### 单季度财务指标 (`230-237`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `230` | `operating_revenue_single_quarter` | `营业收入` | `Single-quarter Operating Revenue` | `single_quarter` |
| `231` | `operating_profit_single_quarter` | `营业利润` | `Single-quarter Operating Profit` | `single_quarter` |
| `232` | `net_profit_attributable_to_owners_of_parent_single_quarter` | `归属于母公司所有者的净利润` | `Single-quarter Net Profit Attributable to Owners of the Parent` | `single_quarter` |
| `233` | `net_profit_after_deducting_non_recurring_profit_and_loss_single_quarter` | `扣除非经常性损益后的净利润` | `Single-quarter Net Profit after Deducting Non-recurring Profit and Loss` | `single_quarter` |
| `234` | `net_cash_flow_from_operating_activities_single_quarter` | `经营活动产生的现金流量净额` | `Single-quarter Net Cash Flow from Operating Activities` | `single_quarter` |
| `235` | `net_cash_flow_from_investing_activities_single_quarter` | `投资活动产生的现金流量净额` | `Single-quarter Net Cash Flow from Investing Activities` | `single_quarter` |
| `236` | `net_cash_flow_from_financing_activities_single_quarter` | `筹资活动产生的现金流量净额` | `Single-quarter Net Cash Flow from Financing Activities` | `single_quarter` |
| `237` | `net_increase_in_cash_and_cash_equivalents_single_quarter` | `现金及现金等价物净增加额` | `Single-quarter Net Increase in Cash and Cash Equivalents` | `single_quarter` |

### 股本股东 (`238-245`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `238` | `total_shares` | `总股本` | `Total Shares` | `shareholder` |
| `239` | `float_a_shares` | `已上市流通A股` | `Float A Shares` | `shareholder` |
| `240` | `float_b_shares` | `已上市流通B股` | `Float B Shares` | `shareholder` |
| `241` | `float_h_shares` | `已上市流通H股` | `Float H Shares` | `shareholder` |
| `242` | `shareholder_count` | `股东人数(户)` | `Number of Shareholders` | `shareholder` |
| `243` | `largest_shareholder_shares_held` | `第一大股东的持股数量` | `Shares Held by the Largest Shareholder` | `shareholder` |
| `244` | `top_ten_float_shareholders_shares_held` | `十大流通股东持股数量合计(股)` | `Aggregate Shares Held by Top Ten Float Shareholders` | `shareholder` |
| `245` | `top_ten_shareholders_shares_held` | `十大股东持股数量合计(股)` | `Aggregate Shares Held by Top Ten Shareholders` | `shareholder` |

### 机构持股 (`246-263`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `246` | `institution_count` | `机构总量（家）` | `Number of Institutional Holders` | `institutional_holding` |
| `247` | `institutional_shares_held` | `机构持股总量(股)` | `Institutional Shares Held` | `institutional_holding` |
| `248` | `qfii_institution_count` | `QFII机构数` | `Number of QFII Institutions` | `institutional_holding` |
| `249` | `qfii_shares_held` | `QFII持股量` | `QFII Shares Held` | `institutional_holding` |
| `250` | `brokerage_institution_count` | `券商机构数` | `Number of Brokerage Institutions` | `institutional_holding` |
| `251` | `brokerage_shares_held` | `券商持股量` | `Brokerage Shares Held` | `institutional_holding` |
| `252` | `insurance_institution_count` | `保险机构数` | `Number of Insurance Institutions` | `institutional_holding` |
| `253` | `insurance_shares_held` | `保险持股量` | `Insurance Shares Held` | `institutional_holding` |
| `254` | `fund_institution_count` | `基金机构数` | `Number of Fund Institutions` | `institutional_holding` |
| `255` | `fund_shares_held` | `基金持股量` | `Fund Shares Held` | `institutional_holding` |
| `256` | `social_security_institution_count` | `社保机构数` | `Number of Social Security Institutions` | `institutional_holding` |
| `257` | `social_security_shares_held` | `社保持股量` | `Social Security Shares Held` | `institutional_holding` |
| `258` | `private_fund_institution_count` | `私募机构数` | `Number of Private Fund Institutions` | `institutional_holding` |
| `259` | `private_fund_shares_held` | `私募持股量` | `Private Fund Shares Held` | `institutional_holding` |
| `260` | `finance_company_institution_count` | `财务公司机构数` | `Number of Finance Company Institutions` | `institutional_holding` |
| `261` | `finance_company_shares_held` | `财务公司持股量` | `Finance Company Shares Held` | `institutional_holding` |
| `262` | `annuity_institution_count` | `年金机构数` | `Number of Annuity Institutions` | `institutional_holding` |
| `263` | `annuity_shares_held` | `年金持股量` | `Annuity Shares Held` | `institutional_holding` |

### 新增指标 (`264-322`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `264` | `top_ten_float_shareholders_a_shares_held` | `十大流通股东中持有A股合计(股) [注：季度报告中，若股东同时持有非流通A股性质的股份(如同时持有流通A股和流通B股），指标264取的是包含同时持有非流通A股性质的流通股数]` | `A Shares Held by Top Ten Float Shareholders` | `shareholder` |
| `265` | `largest_float_shareholder_shares_held` | `第一大流通股东持股量(股)` | `Shares Held by the Largest Float Shareholder` | `shareholder` |
| `266` | `free_float_shares` | `自由流通股(股)[注：1.自由流通股=已流通A股-十大流通股东5%以上的A股；2.季度报告中，若股东同时持有非流通A股性质的股份(如同时持有流通A股和流通H股），5%以上的持股取的是不包含同时持有非流通A股性质的流通股数，结果可能偏大； 3.指标按报告期展示，新股在上市日的下个报告期才有数据]` | `Free-float Shares` | `shareholder` |
| `267` | `restricted_tradable_a_shares` | `受限流通A股(股)` | `Restricted Tradable A Shares` | `shareholder` |
| `268` | `general_risk_reserve` | `一般风险准备(金融类)` | `General Risk Reserve` | `balance_sheet` |
| `269` | `other_comprehensive_income` | `其他综合收益(利润表)` | `Other Comprehensive Income` | `income_statement` |
| `270` | `total_comprehensive_income` | `综合收益总额(利润表)` | `Total Comprehensive Income` | `income_statement` |
| `271` | `equity_attributable_to_owners_of_parent` | `归属于母公司股东权益(资产负债表)` | `Equity Attributable to Owners of the Parent` | `balance_sheet` |
| `272` | `bank_institution_count` | `银行机构数(家)(机构持股)` | `Bank Institution Count` | `institutional_holding` |
| `273` | `bank_institution_shares_held` | `银行持股量(股)(机构持股)` | `Bank Institution Shares Held` | `institutional_holding` |
| `274` | `general_corporate_institution_count` | `一般法人机构数(家)(机构持股)` | `General Corporate Institution Count` | `institutional_holding` |
| `275` | `general_corporate_institution_shares_held` | `一般法人持股量(股)(机构持股)` | `General Corporate Institution Shares Held` | `institutional_holding` |
| `276` | `net_profit_ttm` | `近一年净利润(元)` | `Net Profit (TTM)` | `income_statement` |
| `277` | `trust_institution_count` | `信托机构数(家)(机构持股)` | `Trust Institution Count` | `institutional_holding` |
| `278` | `trust_institution_shares_held` | `信托持股量(股)(机构持股)` | `Trust Institution Shares Held` | `institutional_holding` |
| `279` | `special_corporate_institution_count` | `特殊法人机构数(家)(机构持股)` | `Special Corporate Institution Count` | `institutional_holding` |
| `280` | `special_corporate_institution_shares_held` | `特殊法人持股量(股)(机构持股)` | `Special Corporate Institution Shares Held` | `institutional_holding` |
| `281` | `weighted_roe` | `加权净资产收益率(每股指标)` | `Weighted Return on Equity` | `profitability` |
| `282` | `basic_earnings_per_share_after_deducting_non_recurring_profit_and_loss_single_quarter` | `扣非每股收益(单季度财务指标)` | `Basic Earnings per Share after Deducting Non-recurring Profit and Loss (Single Quarter)` | `single_quarter` |
| `283` | `operating_revenue_ttm` | `最近一年营业收入（万元）` | `Operating Revenue (TTM)` | `income_statement` |
| `284` | `state_backed_institution_shares_held` | `国家队持股数量（万股)[注：本指标统计包含汇金公司、证金公司、外汇管理局旗下投资平台、国家队基金、国开、养老金以及中科汇通等国家队机构持股数量]` | `Shares Held by State-backed Institutions` | `institutional_holding` |
| `285` | `earnings_preview_net_profit_yoy_growth_lower_bound` | `业绩预告-本期净利润同比增幅下限%[注：指标285至294展示未来一个报告期的数据。例，3月31日至6月29日这段时间内展示的是中报的数据；如果最新的财务报告后面有多个报告期的业绩预告/快报，只能展示最新的财务报告后面的一个报告期的业绩预告/快报]` | `Lower Bound of YoY Growth in Net Profit for Earnings Preview` | `earnings_preview` |
| `286` | `earnings_preview_net_profit_yoy_growth_upper_bound` | `业绩预告-本期净利润同比增幅上限%` | `Upper Bound of YoY Growth in Net Profit for Earnings Preview` | `earnings_preview` |
| `287` | `flash_report_net_profit_attributable_to_owners_of_parent` | `业绩快报-归母净利润` | `Net Profit Attributable to Owners of the Parent in Earnings Flash Report` | `earnings_flash_report` |
| `288` | `flash_report_net_profit_after_deducting_non_recurring_profit_and_loss` | `业绩快报-扣非净利润` | `Net Profit after Deducting Non-recurring Profit and Loss in Earnings Flash Report` | `earnings_flash_report` |
| `289` | `flash_report_total_assets` | `业绩快报-总资产` | `Total Assets in Earnings Flash Report` | `earnings_flash_report` |
| `290` | `flash_report_net_assets` | `业绩快报-净资产` | `Net Assets in Earnings Flash Report` | `earnings_flash_report` |
| `291` | `flash_report_earnings_per_share` | `业绩快报-每股收益` | `Earnings per Share in Earnings Flash Report` | `earnings_flash_report` |
| `292` | `flash_report_diluted_roe` | `业绩快报-摊薄净资产收益率` | `Diluted Return on Equity in Earnings Flash Report` | `earnings_flash_report` |
| `293` | `flash_report_weighted_roe` | `业绩快报-加权净资产收益率` | `Weighted Return on Equity in Earnings Flash Report` | `earnings_flash_report` |
| `294` | `flash_report_book_value_per_share` | `业绩快报-每股净资产` | `Book Value per Share in Earnings Flash Report` | `earnings_flash_report` |
| `295` | `notes_and_accounts_payable` | `应付票据及应付账款(资产负债表)` | `Notes and Accounts Payable` | `balance_sheet` |
| `296` | `notes_and_accounts_receivable` | `应收票据及应收账款(资产负债表)` | `Notes and Accounts Receivable` | `balance_sheet` |
| `297` | `non_current_deferred_income` | `递延收益(资产负债表-非流动负债)` | `Non-current Deferred Income` | `balance_sheet` |
| `298` | `other_comprehensive_income_in_equity` | `其他综合收益(资产负债表)` | `Other Comprehensive Income in Equity` | `balance_sheet` |
| `299` | `other_equity_instruments` | `其他权益工具(资产负债表)` | `Other Equity Instruments` | `balance_sheet` |
| `300` | `other_income` | `其他收益(利润表)` | `Other Income` | `income_statement` |
| `301` | `asset_disposal_income` | `资产处置收益(利润表)` | `Asset Disposal Income` | `income_statement` |
| `302` | `net_profit_from_continuing_operations` | `持续经营净利润(利润表)` | `Net Profit from Continuing Operations` | `income_statement` |
| `303` | `net_profit_from_discontinued_operations` | `终止经营净利润(利润表)` | `Net Profit from Discontinued Operations` | `income_statement` |
| `304` | `research_and_development_expenses` | `研发费用(利润表)` | `Research and Development Expenses` | `income_statement` |
| `305` | `interest_expense_in_finance_expenses` | `其中:利息费用(利润表-财务费用)` | `Interest Expense in Finance Expenses` | `income_statement` |
| `306` | `interest_income_in_finance_expenses` | `其中:利息收入(利润表-财务费用)` | `Interest Income in Finance Expenses` | `income_statement` |
| `307` | `net_cash_flow_from_operating_activities_ttm` | `近一年经营活动现金流净额` | `Net Cash Flow from Operating Activities (TTM)` | `cash_flow_statement` |
| `308` | `net_profit_attributable_to_owners_of_parent_ttm` | `近一年归母净利润（万元）` | `Net Profit Attributable to Owners of the Parent (TTM)` | `income_statement` |
| `309` | `net_profit_after_deducting_non_recurring_profit_and_loss_ttm` | `近一年扣非净利润（万元）` | `Net Profit after Deducting Non-recurring Profit and Loss (TTM)` | `income_statement` |
| `310` | `net_increase_in_cash_and_cash_equivalents_ttm` | `近一年现金净流量（万元）` | `Net Increase in Cash and Cash Equivalents (TTM)` | `cash_flow_statement` |
| `311` | `basic_earnings_per_share_single_quarter` | `基本每股收益（单季度）` | `Basic Earnings per Share (Single Quarter)` | `single_quarter` |
| `312` | `total_operating_revenue_single_quarter` | `营业总收入(单季度)(万元)` | `Total Operating Revenue (Single Quarter)` | `single_quarter` |
| `313` | `earnings_preview_announcement_date` | `业绩预告公告日期 [注：本指标展示未来一个报告期的数据。例,3月31日至6月29日这段时间内展示的是中报的数据；如果最新的财务报告后面有多个报告期的业绩预告/快报，只能展示最新的财务报告后面的一个报告期的业绩预告/快报的数据；公告日期格式为YYMMDD，例：190101代表2019年1月1日]` | `Earnings Preview Announcement Date` | `earnings_preview` |
| `314` | `financial_report_announcement_date` | `财报公告日期 [注：日期格式为YYMMDD,例：190101代表2019年1月1日]` | `Financial Report Announcement Date` | `disclosure` |
| `315` | `flash_report_announcement_date` | `业绩快报公告日期 [注：本指标展示未来一个报告期的数据。例,3月31日至6月29日这段时间内展示的是中报的数据；如果最新的财务报告后面有多个报告期的业绩预告/快报，只能展示最新的财务报告后面的一个报告期的业绩预告/快报的数据；公告日期格式为YYMMDD，例：190101代表2019年1月1日]` | `Earnings Flash Report Announcement Date` | `earnings_flash_report` |
| `316` | `net_cash_flow_from_investing_activities_ttm` | `近一年投资活动现金流净额(万元)` | `Net Cash Flow from Investing Activities (TTM)` | `cash_flow_statement` |
| `317` | `earnings_preview_net_profit_lower_bound` | `业绩预告-本期净利润下限(万元)[注：指标317至318展示未来一个报告期的数据。例，3月31日至6月29日这段时间内展示的是中报的数据；如果最新的财务报告后面有多个报告期的业绩预告/快报，只能展示最新的财务报告后面的一个报告期的业绩预告/快报]` | `Lower Bound of Net Profit for Earnings Preview` | `earnings_preview` |
| `318` | `earnings_preview_net_profit_upper_bound` | `业绩预告-本期净利润上限(万元)` | `Upper Bound of Net Profit for Earnings Preview` | `earnings_preview` |
| `319` | `total_operating_revenue_ttm` | `营业总收入TTM(万元)` | `Total Operating Revenue (TTM)` | `income_statement` |
| `320` | `employee_count` | `员工总数(人)` | `Employee Count` | `disclosure` |
| `321` | `free_cash_flow_to_firm_per_share` | `每股企业自由现金流` | `Free Cash Flow to Firm per Share` | `per_share` |
| `322` | `free_cash_flow_to_equity_per_share` | `每股股东自由现金流` | `Free Cash Flow to Equity per Share` | `per_share` |

### 资产负债表新增指标 (`401-439`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `401` | `special_reserve` | `专项储备(万元)` | `Special Reserve` | `balance_sheet` |
| `402` | `settlement_reserve_fund` | `结算备付金(万元)` | `Settlement Reserve Fund` | `balance_sheet` |
| `403` | `funds_lent` | `拆出资金(万元)` | `Funds Lent` | `balance_sheet` |
| `404` | `loans_and_advances_current` | `发放贷款及垫款(万元)(流动资产科目)` | `Loans and Advances under Current Assets` | `balance_sheet` |
| `405` | `derivative_financial_assets` | `衍生金融资产(万元)` | `Derivative Financial Assets` | `balance_sheet` |
| `406` | `premiums_receivable` | `应收保费(万元)` | `Premiums Receivable` | `balance_sheet` |
| `407` | `reinsurance_receivables` | `应收分保账款(万元)` | `Reinsurance Receivables` | `balance_sheet` |
| `408` | `reinsurance_contract_reserves_receivable` | `应收分保合同准备金(万元)` | `Reinsurance Contract Reserves Receivable` | `balance_sheet` |
| `409` | `financial_assets_purchased_under_resale_agreements` | `买入返售金融资产(万元)` | `Financial Assets Purchased under Resale Agreements` | `balance_sheet` |
| `410` | `assets_held_for_sale` | `划分为持有待售的资产(万元)` | `Assets Held for Sale` | `balance_sheet` |
| `411` | `loans_and_advances_non_current` | `发放贷款及垫款(万元)(非流动资产科目)` | `Loans and Advances under Non-current Assets` | `balance_sheet` |
| `412` | `borrowings_from_central_bank` | `向中央银行借款(万元)` | `Borrowings from Central Bank` | `balance_sheet` |
| `413` | `deposits_from_customers_and_interbank` | `吸收存款及同业存放(万元)` | `Deposits from Customers and Interbank` | `balance_sheet` |
| `414` | `funds_borrowed` | `拆入资金(万元)` | `Funds Borrowed` | `balance_sheet` |
| `415` | `derivative_financial_liabilities` | `衍生金融负债(万元)` | `Derivative Financial Liabilities` | `balance_sheet` |
| `416` | `financial_assets_sold_under_repurchase_agreements` | `卖出回购金融资产款(万元)` | `Financial Assets Sold under Repurchase Agreements` | `balance_sheet` |
| `417` | `fees_and_commissions_payable` | `应付手续费及佣金(万元)` | `Fees and Commissions Payable` | `balance_sheet` |
| `418` | `reinsurance_payables` | `应付分保账款(万元)` | `Reinsurance Payables` | `balance_sheet` |
| `419` | `insurance_contract_reserves` | `保险合同准备金(万元)` | `Insurance Contract Reserves` | `balance_sheet` |
| `420` | `funds_received_as_agent_of_stock_exchange` | `代理买卖证券款(万元)` | `Funds Received as Agent of Stock Exchange` | `balance_sheet` |
| `421` | `funds_received_as_securities_underwriter` | `代理承销证券款(万元)` | `Funds Received as Securities Underwriter` | `balance_sheet` |
| `422` | `liabilities_held_for_sale` | `划分为持有待售的负债(万元)` | `Liabilities Held for Sale` | `balance_sheet` |
| `423` | `provisions_extended_balance_sheet` | `预计负债(万元)` | `Provisions (Extended Balance Sheet Variant)` | `balance_sheet` |
| `424` | `deferred_income_current` | `递延收益(万元)（流动负债科目，公告此科目的股票较少，大部分公司没有此数据）` | `Deferred Income under Current Liabilities` | `balance_sheet` |
| `425` | `preferred_shares_non_current_liabilities` | `其中:优先股(万元)(非流动负债科目)` | `Preferred Shares under Non-current Liabilities` | `balance_sheet` |
| `426` | `perpetual_bonds_non_current_liabilities` | `永续债(万元)(非流动负债科目)` | `Perpetual Bonds under Non-current Liabilities` | `balance_sheet` |
| `427` | `long_term_employee_compensation_payable` | `长期应付职工薪酬(万元)` | `Long-term Employee Compensation Payable` | `balance_sheet` |
| `428` | `preferred_shares_equity` | `其中:优先股(万元)(所有者权益科目)` | `Preferred Shares under Equity` | `balance_sheet` |
| `429` | `perpetual_bonds_equity` | `永续债(万元)(所有者权益科目)` | `Perpetual Bonds under Equity` | `balance_sheet` |
| `430` | `debt_investments` | `债权投资(万元)` | `Debt Investments` | `balance_sheet` |
| `431` | `other_debt_investments` | `其他债权投资(万元)` | `Other Debt Investments` | `balance_sheet` |
| `432` | `other_equity_instrument_investments` | `其他权益工具投资(万元)` | `Other Equity Instrument Investments` | `balance_sheet` |
| `433` | `other_non_current_financial_assets` | `其他非流动金融资产(万元)` | `Other Non-current Financial Assets` | `balance_sheet` |
| `434` | `contract_liabilities` | `合同负债(万元)` | `Contract Liabilities` | `balance_sheet` |
| `435` | `contract_assets` | `合同资产(万元)` | `Contract Assets` | `balance_sheet` |
| `436` | `other_assets` | `其他资产(万元)` | `Other Assets` | `balance_sheet` |
| `437` | `receivables_financing` | `应收款项融资(万元)` | `Receivables Financing` | `balance_sheet` |
| `438` | `right_of_use_assets` | `使用权资产(万元)` | `Right-of-use Assets` | `balance_sheet` |
| `439` | `lease_liabilities` | `租赁负债(万元)` | `Lease Liabilities` | `balance_sheet` |

### 利润表新增指标 (`501-521`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `501` | `diluted_earnings_per_share` | `稀释每股收益(元)` | `Diluted Earnings per Share` | `income_statement` |
| `502` | `total_operating_revenue` | `营业总收入(万元)` | `Total Operating Revenue` | `income_statement` |
| `503` | `foreign_exchange_gains` | `汇兑收益(万元)` | `Foreign Exchange Gains` | `income_statement` |
| `504` | `comprehensive_income_attributable_to_owners_of_parent` | `其中:归属于母公司综合收益(万元)` | `Comprehensive Income Attributable to Owners of the Parent` | `income_statement` |
| `505` | `comprehensive_income_attributable_to_non_controlling_interests` | `其中:归属于少数股东综合收益(万元)` | `Comprehensive Income Attributable to Non-controlling Interests` | `income_statement` |
| `506` | `interest_income` | `利息收入(万元)` | `Interest Income` | `income_statement` |
| `507` | `earned_premiums` | `已赚保费(万元)` | `Earned Premiums` | `income_statement` |
| `508` | `fee_and_commission_income` | `手续费及佣金收入(万元)` | `Fee and Commission Income` | `income_statement` |
| `509` | `interest_expense` | `利息支出(万元)` | `Interest Expense` | `income_statement` |
| `510` | `fee_and_commission_expenses` | `手续费及佣金支出(万元)` | `Fee and Commission Expenses` | `income_statement` |
| `511` | `surrender_value` | `退保金(万元)` | `Surrender Value` | `income_statement` |
| `512` | `net_claims_incurred` | `赔付支出净额(万元)` | `Net Claims Incurred` | `income_statement` |
| `513` | `net_increase_in_insurance_contract_reserves` | `提取保险合同准备金净额(万元)` | `Net Increase in Insurance Contract Reserves` | `income_statement` |
| `514` | `policyholder_dividend_expenses` | `保单红利支出(万元)` | `Policyholder Dividend Expenses` | `income_statement` |
| `515` | `reinsurance_expenses` | `分保费用(万元)` | `Reinsurance Expenses` | `income_statement` |
| `516` | `gain_on_disposal_of_non_current_assets` | `其中:非流动资产处置利得(万元)` | `Gain on Disposal of Non-current Assets` | `income_statement` |
| `517` | `credit_impairment_losses` | `信用减值损失(万元)` | `Credit Impairment Losses` | `income_statement` |
| `518` | `net_exposure_hedging_gains` | `净敞口套期收益(万元)` | `Net Exposure Hedging Gains` | `income_statement` |
| `519` | `total_operating_costs` | `营业总成本(万元)` | `Total Operating Costs` | `income_statement` |
| `520` | `credit_impairment_losses_2019_format` | `信用减值损失(万元、2019格式)` | `Credit Impairment Losses, 2019 Format` | `income_statement` |
| `521` | `asset_impairment_losses_2019_format` | `资产减值损失(万元、2019格式)` | `Asset Impairment Losses, 2019 Format` | `income_statement` |

### 现金流量表新增指标 (`561-580`)

| source_field_id | field_code | field_name_cn | field_name_en | category |
| --- | --- | --- | --- | --- |
| `561` | `effect_of_other_reasons_on_cash_for_closing_cash_balance` | `加:其他原因对现金的影响2(万元)(现金的期末余额科目)` | `Effect of Other Reasons on Cash for Closing Cash Balance` | `cash_flow_statement` |
| `562` | `net_increase_in_customer_deposits_and_interbank_deposits` | `客户存款和同业存放款项净增加额(万元)` | `Net Increase in Customer Deposits and Interbank Deposits` | `cash_flow_statement` |
| `563` | `net_increase_in_borrowings_from_central_bank` | `向中央银行借款净增加额(万元)` | `Net Increase in Borrowings from the Central Bank` | `cash_flow_statement` |
| `564` | `net_increase_in_funds_borrowed_from_other_financial_institutions` | `向其他金融机构拆入资金净增加额(万元)` | `Net Increase in Funds Borrowed from Other Financial Institutions` | `cash_flow_statement` |
| `565` | `cash_received_from_premiums_of_original_insurance_contracts` | `收到原保险合同保费取得的现金(万元)` | `Cash Received from Premiums of Original Insurance Contracts` | `cash_flow_statement` |
| `566` | `net_cash_received_from_reinsurance_business` | `收到再保险业务现金净额(万元)` | `Net Cash Received from Reinsurance Business` | `cash_flow_statement` |
| `567` | `net_increase_in_policyholder_deposits_and_investment_funds` | `保户储金及投资款净增加额(万元)` | `Net Increase in Policyholder Deposits and Investment Funds` | `cash_flow_statement` |
| `568` | `net_increase_in_proceeds_from_disposal_of_financial_assets_at_fair_value_through_profit_or_loss` | `处置以公允价值计量且其变动计入当期损益的金融资产净增加额(万元)` | `Net Increase in Proceeds from Disposal of Financial Assets at Fair Value through Profit or Loss` | `cash_flow_statement` |
| `569` | `cash_received_from_interest_fees_and_commissions` | `收取利息、手续费及佣金的现金(万元)` | `Cash Received from Interest, Fees and Commissions` | `cash_flow_statement` |
| `570` | `net_increase_in_funds_borrowed` | `拆入资金净增加额(万元)` | `Net Increase in Funds Borrowed` | `cash_flow_statement` |
| `571` | `net_increase_in_repurchase_business_funds` | `回购业务资金净增加额(万元)` | `Net Increase in Repurchase Business Funds` | `cash_flow_statement` |
| `572` | `net_increase_in_loans_and_advances_to_customers` | `客户贷款及垫款净增加额(万元)` | `Net Increase in Loans and Advances to Customers` | `cash_flow_statement` |
| `573` | `net_increase_in_deposits_with_central_bank_and_other_banks` | `存放中央银行和同业款项净增加额(万元)` | `Net Increase in Deposits with the Central Bank and Other Banks` | `cash_flow_statement` |
| `574` | `cash_paid_for_claims_under_original_insurance_contracts` | `支付原保险合同赔付款项的现金(万元)` | `Cash Paid for Claims under Original Insurance Contracts` | `cash_flow_statement` |
| `575` | `cash_paid_for_interest_fees_and_commissions` | `支付利息、手续费及佣金的现金(万元)` | `Cash Paid for Interest, Fees and Commissions` | `cash_flow_statement` |
| `576` | `cash_paid_for_policyholder_dividends` | `支付保单红利的现金(万元)` | `Cash Paid for Policyholder Dividends` | `cash_flow_statement` |
| `577` | `cash_received_by_subsidiaries_from_capital_contributions_by_non_controlling_interests` | `其中:子公司吸收少数股东投资收到的现金(万元)` | `Cash Received by Subsidiaries from Capital Contributions by Non-controlling Interests` | `cash_flow_statement` |
| `578` | `cash_paid_by_subsidiaries_for_dividends_and_profit_distribution_to_non_controlling_interests` | `其中:子公司支付给少数股东的股利、利润(万元)` | `Cash Paid by Subsidiaries for Dividends and Profit Distribution to Non-controlling Interests` | `cash_flow_statement` |
| `579` | `depreciation_and_amortization_of_investment_property` | `投资性房地产的折旧及摊销(万元)` | `Depreciation and Amortization of Investment Property` | `cash_flow_statement` |
| `580` | `credit_impairment_losses_for_cash_flow_reconciliation` | `信用减值损失(万元)` | `Credit Impairment Losses for Cash Flow Reconciliation` | `cash_flow_statement` |
