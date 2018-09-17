
var loc = window.location, ws_uri;
if (loc.protocol === "https:") {
    ws_uri = "wss:";
} else {
    ws_uri = "ws:";
}
ws_uri += "//" + loc.host;
var path = loc.pathname.replace(/\/$/, '');
ws_uri += path + "/websocket";

var ws = new WebSocket(ws_uri);

$(function(){

	$("#go-button").click(function() {
		if (ws.readyState === 1) {
			$("#output").show();
			var url = $("#url").val();
			var val = {Key: 'url', Value: url};
			ws.send(JSON.stringify(val));
			$("#status").append("Requesting URL " + url + "\n");
		} else {
			$("#status").append("socket not ready\n")
		}
	});

	/*
	ws.onopen = function() {
	};
	*/

	ws.onmessage = function (e)	{
		var msg = JSON.parse(e.data);
		if( 'Key' in msg ) {
			switch (msg.Key) {
				case 'error':
					$("#status").append("Error: " + msg.Value + "\n");
					break;
				case 'progress':
					var pct = parseFloat(msg.Value.Pct);
					$("#progress-bar > span").css("width", pct + "%")
						.text(pct + "%");
					$("#eta").text( msg.Value.ETA );
					break;
				case 'info':
					$("#title").text( msg.Value.Title );
					var bytes = parseFloat(msg.Value.FileSize)
					$("#filesize").text( (bytes / 1024 / 1024).toFixed(2) + " MB" );
					break;
				case 'link':
					$("#link").attr('href', encodeURI(msg.Value.DownloadURL))
						.css('display', 'inline-block');
					$("#progress-bar > span").css("width", "100%")
						.text("100%");
					break;
			}
		}
	};

	ws.onclose = function()	{
			$("#status").append("Connection closed\n");
	};

});
