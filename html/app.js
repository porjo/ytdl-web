
var loc = window.location, ws_uri;
if (loc.protocol === "https:") {
    ws_uri = "wss:";
} else {
    ws_uri = "ws:";
}
ws_uri += "//" + loc.host;
var path = loc.pathname.replace(/\/$/, '');
ws_uri += path + "/websocket";

var progTimer = null;
var progLast = null;

$(function(){
	var ws = new WebSocket(ws_uri);

	var searchParams = new URLSearchParams(window.location.search);
	var inputURL = searchParams.get('url');
	if (inputURL) {
		$("#url").val(inputURL);
	}

	$("#go-button").click(function() {
		if (ws.readyState === 1) {
			$("#spinner").show();
			$("#input-form").hide();
			$("#progress-bar > span").css("width", "0%")
				.text("0%");
			var url = $("#url").val();
			var forceOpus = $("#force-opus").is(":checked");
			var val = {URL: url, ForceOpus: forceOpus};
			ws.send(JSON.stringify(val));
			$("#status").append("Requesting URL " + url + "\n");
			$(this).prop('disabled', true);
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
			$("#output").show();
			switch (msg.Key) {
				case 'error':
					$("#spinner").hide();
					$("#status").append("Error: " + msg.Value + "\n");
					break;
				case 'unknown':
					$("#status").prepend(msg.Value);
					break;
				case 'progress':
					$("#spinner").hide();
					$("#progress-bar").show();
					progLast = Date.now();
					if(!progTimer) {
						progTimer = setInterval(() => {
							let now = Date.now();
							if( (now - progLast) > 4000) {
								$("#spinner").show();
								$("#progress-bar").hide();
							}
						},1000);
					}
					var pct = parseFloat(msg.Value.Pct);
					$("#progress-bar > span").css("width", pct + "%")
						.text(pct + "%");
					$("#eta").text( msg.Value.ETA );
					if( msg.Value.FileSize !== '' ) {
						$("#filesize").text( msg.Value.FileSize + " MB" );
					}
					break;
				case 'info':
					$("#title").text( msg.Value.Title );
					var bytes = parseFloat(msg.Value.FileSize)
					$("#filesize").text( (bytes / 1024 / 1024).toFixed(2) + " MB" );
					break;
				case 'link':
					clearTimeout(progTimer);
					$("#spinner").hide();
					var $link = $("<a>")
						.attr("href", encodeURI(msg.Value.DownloadURL))
						.attr("download", "")
						.text(msg.Value.DownloadURL);
					$("#links").append($link);
					$("#links").append("<br>");
					$("#progress-bar > span").css("width", "100%")
						.text("100%");
					$("#go-button").prop('disabled', false);
					break;
			}
		}
	};

	ws.onclose = function()	{
		$("#status").append("Connection closed\n");
		console.log("Connection closed");
		$("#ws-status-light").toggleClass("on off");
	};

	ws.onopen = function(e) {
		$("#status").append("Connection opened\n");
		console.log("Connection opened");
		$("#ws-status-light").toggleClass("off on");
	}
});
