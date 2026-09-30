package service

// telegramNicks is the one place Search learns Telegram @nicks: the nick of
// each of tgIds that has one, with or without its '@'. Telegram accounts
// arrive with #210 (decision #186 point 1: tg_accounts, the nick refreshed
// on every contact); until that lands nobody has a nick and the lookup
// answers nothing. #210 or its follow-up points this at tg_accounts in one
// query for all the ids.
var telegramNicks = func(tgIds []int64) map[int64]string {
	return nil
}
