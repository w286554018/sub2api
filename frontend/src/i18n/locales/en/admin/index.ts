import groupCacheTest from './groupCacheTest'
import requestCapture from './requestCapture'
import astraGateway from './astraGateway'
import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import promptAudit from './promptAudit'
import promptRules from './promptRules'
import plugins from './plugins'
import intelligentTests from './intelligentTests'
import accountHealth from './accountHealth'
import harvestFlow from './harvestFlow'

export default {
  ...groupCacheTest,
  ...requestCapture,
  ...astraGateway,
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...promptAudit,
  ...promptRules,
  ...plugins,
  ...intelligentTests,
  ...accountHealth,
  ...harvestFlow,
}
